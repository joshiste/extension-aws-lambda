package extlambda

import (
  "context"
  "encoding/json"
  "errors"
  "fmt"
  "github.com/aws/aws-sdk-go-v2/config"
  "github.com/aws/aws-sdk-go-v2/service/ssm"
  "github.com/aws/aws-sdk-go-v2/service/ssm/types"
  "github.com/steadybit/action-kit/go/action_kit_api/v2"
  extension_kit "github.com/steadybit/extension-kit"
  "github.com/steadybit/extension-kit/extconversion"
  "github.com/steadybit/extension-kit/exthttp"
  "github.com/steadybit/extension-kit/extutil"
  "net/http"
)

const actionBasePath = basePath + "actions/inject-failure"

func RegisterActionHandlers() {
  exthttp.RegisterHttpHandler(actionBasePath, exthttp.GetterAsHandler(getActionDescription))
  exthttp.RegisterHttpHandler(actionBasePath+"/prepare", prepare)
  exthttp.RegisterHttpHandler(actionBasePath+"/start", start)
  exthttp.RegisterHttpHandler(actionBasePath+"/stop", stop)
}

func GetActionEndpoints() action_kit_api.ActionList {
  return action_kit_api.ActionList{
    Actions: []action_kit_api.DescribingEndpointReference{
      {
        Method: "GET",
        Path:   actionBasePath,
      },
    },
  }
}

func getActionDescription() action_kit_api.ActionDescription {
  return action_kit_api.ActionDescription{
    Id:          fmt.Sprintf("%s.statusCode", targetID),
    Label:       "Inject Status Code",
    Description: "Returns a fixed status code.",

    // Version of the target type; this used for caching
    // When doing changes the version should be bumped.
    // When developing the SNAPSHOT suffix will prevent
    Version: "1.0.0-SNAPSHOT",
    Icon:    extutil.Ptr(targetIcon),

    // The target type this action is for
    TargetType: extutil.Ptr(targetID),

    // You can provide a list of target templates to help the user select targets.
    // A template can be used to pre-fill a selection.
    TargetSelectionTemplates: extutil.Ptr([]action_kit_api.TargetSelectionTemplate{
      {
        Label: "by function name",
        Query: "aws.lambda.function-name=\"\"",
      },
    }),

    // Category for the targets to appear in
    Category: extutil.Ptr("cloud"),
    Kind:     action_kit_api.Attack,

    // How the action is controlled over time.
    //   External: The agent takes care and calls stop then the time has passed. Requires a duration parameter. Use this when the duration is known in advance.
    //   Internal: The action hast to implement the status endpoint to signal when the action is done. Use this when the duration is not known in advance.
    //   Instantaneous: The action is done immediately. Use this for actions that happen immediately, e.g. a reboot.
    TimeControl: action_kit_api.External,

    // The parameters for the action
    Parameters: []action_kit_api.ActionParameter{
      {
        Label:        "Duration",
        Name:         "duration",
        Type:         "duration",
        Description:  extutil.Ptr("The duration of the attack."),
        Advanced:     extutil.Ptr(false),
        Required:     extutil.Ptr(true),
        DefaultValue: extutil.Ptr("30s"),
        Order:        extutil.Ptr(0),
      },
      {
        Name:         "statuscode",
        Label:        "Status Code",
        Description:  extutil.Ptr("The status code to return."),
        Type:         action_kit_api.Integer,
        DefaultValue: extutil.Ptr("500"),
        Required:     extutil.Ptr(true),
        Order:        extutil.Ptr(1),
      },
      {
        Name:         "rate",
        Label:        "Rate",
        Description:  extutil.Ptr("The rate of failures to inject."),
        Type:         action_kit_api.Percentage,
        DefaultValue: extutil.Ptr("100"),
        Required:     extutil.Ptr(true),
        Order:        extutil.Ptr(1),
      },
    },
    Prepare: action_kit_api.MutatingEndpointReference{
      Method: "POST",
      Path:   actionBasePath + "/prepare",
    },
    Start: action_kit_api.MutatingEndpointReference{
      Method: "POST",
      Path:   actionBasePath + "/start",
    },
    Stop: extutil.Ptr(action_kit_api.MutatingEndpointReference{
      Method: "POST",
      Path:   actionBasePath + "/stop",
    }),
  }
}

type failureInjectionConfig struct {
  FailureMode string  `json:"failureMode"`
  Rate        float64 `json:"rate"`
  StatusCode  int     `json:"statusCode"`
  IsEnabled   bool    `json:"isEnabled"`
}

type LambdaActionState struct {
  Param  string                 `json:"param"`
  Config failureInjectionConfig `json:"config"`
}

func prepare(w http.ResponseWriter, _ *http.Request, body []byte) {
  var request action_kit_api.PrepareActionRequestBody
  err := json.Unmarshal(body, &request)
  if err != nil {
    exthttp.WriteError(w, extension_kit.ToError("Failed to parse request body", err))
    return
  }

  state, extErr := prepareState(&request)
  if extErr != nil {
    exthttp.WriteError(w, *extErr)
  }

  var convertedState action_kit_api.ActionState
  err = extconversion.Convert(state, &convertedState)
  if err != nil {
    exthttp.WriteError(w, extension_kit.ToError("Failed to encode action state", err))
    return
  }

  exthttp.WriteBody(w, action_kit_api.PrepareResult{
    State: convertedState,
  })
}

func prepareState(request *action_kit_api.PrepareActionRequestBody) (*LambdaActionState, *extension_kit.ExtensionError) {
  failureInjectionParam := request.Target.Attributes["aws.lambda.failure-injection-param"]
  if failureInjectionParam == nil || len(failureInjectionParam) == 0 {
    return nil, extutil.Ptr(extension_kit.ToError("Target is missing the 'aws.lambda.failure-injection-param' attribute. Did you wrap the lambda with https://github.com/gunnargrosch/failure-lambda ?", nil))
  }

  state := &LambdaActionState{
    Param: failureInjectionParam[0],
    Config: failureInjectionConfig{
      FailureMode: "statuscode",
      Rate:        request.Config["rate"].(float64) / 100.0,
      StatusCode:  int(request.Config["statuscode"].(float64)),
      IsEnabled:   true,
    },
  }
  return state, nil
}

func start(w http.ResponseWriter, r *http.Request, body []byte) {
  var request action_kit_api.StartActionRequestBody
  err := json.Unmarshal(body, &request)
  if err != nil {
    exthttp.WriteError(w, extension_kit.ToError("Failed to parse request body", err))
    return
  }

  var state LambdaActionState
  err = extconversion.Convert(request.State, &state)
  if err != nil {
    exthttp.WriteError(w, extension_kit.ToError("Failed to convert log action state", err))
    return
  }

  extErr := putFailureInjectionParameter(r.Context(), state)
  if extErr != nil {
    exthttp.WriteError(w, *extErr)
  }

  exthttp.WriteBody(w, action_kit_api.StartResult{})
}

func putFailureInjectionParameter(ctx context.Context, state LambdaActionState) *extension_kit.ExtensionError {
  value, err := json.Marshal(state.Config)
  if err != nil {
    return extutil.Ptr(extension_kit.ToError("Failed to convert ssm parameter", err))
  }

  client, err := createSsmClient(ctx)
  if err != nil {
    return extutil.Ptr(extension_kit.ToError("Failed to create ssm client", err))
  }

  _, err = client.PutParameter(ctx, &ssm.PutParameterInput{
    Name:        extutil.Ptr(state.Param),
    Value:       extutil.Ptr(string(value)),
    Type:        types.ParameterTypeString,
    DataType:    extutil.Ptr("text"),
    Description: extutil.Ptr("lambda failure injection config - set by steadybit"),
    Overwrite:   extutil.Ptr(true),
  })
  if err != nil {
    return extutil.Ptr(extension_kit.ToError("Failed to put ssm parameter", err))
  }

  _, err = client.AddTagsToResource(ctx, &ssm.AddTagsToResourceInput{
    ResourceId:   extutil.Ptr(state.Param),
    ResourceType: types.ResourceTypeForTaggingParameter,
    Tags:         []types.Tag{{Key: extutil.Ptr("created-by"), Value: extutil.Ptr("steadybit")}},
  })
  if err != nil {
    //ignore error
  }
  return nil
}

func stop(w http.ResponseWriter, r *http.Request, body []byte) {
  var request action_kit_api.StopActionRequestBody
  err := json.Unmarshal(body, &request)
  if err != nil {
    exthttp.WriteError(w, extension_kit.ToError("Failed to parse request body", err))
    return
  }

  var state LambdaActionState
  err = extconversion.Convert(request.State, &state)
  if err != nil {
    exthttp.WriteError(w, extension_kit.ToError("Failed to convert log action state", err))
    return
  }

  extErr := deleteFailureInjectionParameter(r.Context(), state)
  if extErr != nil {
    exthttp.WriteError(w, *extErr)
    return
  }

  exthttp.WriteBody(w, action_kit_api.StopResult{})
}

func deleteFailureInjectionParameter(ctx context.Context, state LambdaActionState) *extension_kit.ExtensionError {
  client, err := createSsmClient(ctx)
  if err != nil {
    return extutil.Ptr(extension_kit.ToError("Failed to create ssm client", err))
  }

  _, err = client.DeleteParameter(ctx, &ssm.DeleteParameterInput{
    Name: extutil.Ptr(state.Param),
  })
  if err != nil {
    var notFound *types.ParameterNotFound
    if !errors.As(err, &notFound) {
      return extutil.Ptr(extension_kit.ToError("Failed to delete ssm parameter", err))
    }
  }

  return nil
}

func createSsmClient(ctx context.Context) (*ssm.Client, error) {
  awsConfig, err := config.LoadDefaultConfig(ctx)
  if err != nil {
    return nil, err
  }
  client := ssm.NewFromConfig(awsConfig)
  return client, err
}
