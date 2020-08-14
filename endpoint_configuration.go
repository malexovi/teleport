package main

import (
	"errors"
	"fmt"
	"os"
	"strconv"

	starlarkextensions "github.com/hundredwatt/teleport/starlarkextensions"
	"go.starlark.net/starlark"
	"gopkg.in/validator.v2"
)

type Endpoint struct {
	URL             string `validate:"regexp=^[hH][tT][tT][pP][sS]?://"`
	Method          string `validate:"in=get|post"`
	BasicAuth       *map[string]string
	Headers         map[string]string
	ResponseType    string `validate:"in=json|csv"`
	Functions       starlark.StringDict
	TableDefinition *map[string]string
	ErrorHandling   *map[errorClass]ExitCode
	LoadOptions     LoadOptions
}

func readEndpointConfiguration(path string, endpointptr *Endpoint) error {
	portFile, err := findPortFile(path, []string{apisConfigDirectory, legacyApisConfigDirectory})
	if err != nil {
		return err
	}

	endpoint := Endpoint{}

	configuration, err := starlark.ExecFile(GetThread(), portFile, nil, predeclared(&endpoint))
	if err != nil {
		return err
	}
	endpoint.Functions = configuration

	if err := endpoint.validate(); err != nil {
		return err
	}

	*endpointptr = endpoint
	return nil
}

func predeclared(endpoint *Endpoint) starlark.StringDict {
	predeclared := starlarkextensions.GetExtensions()
	// DSL Methods
	predeclared["Get"] = starlark.NewBuiltin("Get", endpoint.get)
	predeclared["AddHeader"] = starlark.NewBuiltin("AddHeader", endpoint.addHeader)
	predeclared["BasicAuth"] = starlark.NewBuiltin("BasicAuth", endpoint.setBasicAuth)
	predeclared["ResponseType"] = starlark.NewBuiltin("setResponseType", endpoint.setResponseType)
	predeclared["TableDefinition"] = starlark.NewBuiltin("TableDefinition", endpoint.setTableDefinition)
	predeclared["LoadStrategy"] = starlark.NewBuiltin("LoadStrategy", endpoint.setLoadStrategy)
	predeclared["ErrorHandling"] = starlark.NewBuiltin("ErrorHandling", endpoint.setErrorHandling)

	// Load Strategies
	predeclared["Full"] = starlark.String(Full)
	predeclared["Incremental"] = starlark.String(Incremental)
	predeclared["ModifiedOnly"] = starlark.String(ModifiedOnly)

	// Error Handling
	predeclared["Fail"] = starlark.MakeInt(int(Fail))
	predeclared["Retry"] = starlark.MakeInt(int(Retry))
	predeclared["NetworkError"] = starlark.String("NetworkError")
	predeclared["Http4XXError"] = starlark.String("Http4XXError")
	predeclared["Http5XXError"] = starlark.String("Http5XXError")
	predeclared["InvalidBodyError"] = starlark.String("InvalidBodyError")

	return predeclared
}

func (endpoint *Endpoint) validate() error {
	validator.SetValidationFunc("in", validateIn)
	if errs := validator.Validate(endpoint); errs != nil {
		return fmt.Errorf("Invalid Configuration: %s", errs.Error())
	}

	return nil
}

func (endpoint *Endpoint) get(thread *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (value starlark.Value, err error) {
	var (
		url starlark.String
	)
	if err := starlark.UnpackPositionalArgs("Get", args, kwargs, 1, &url); err != nil {
		return nil, err
	}

	endpoint.URL = os.ExpandEnv(url.GoString())
	endpoint.Method = "GET"

	return starlark.None, nil
}

func (endpoint *Endpoint) addHeader(thread *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (value starlark.Value, err error) {
	var (
		name, hvalue starlark.String
	)
	if err := starlark.UnpackPositionalArgs("BasicAuth", args, kwargs, 2, &name, &hvalue); err != nil {
		return nil, err
	}

	if len(endpoint.Headers) == 0 {
		endpoint.Headers = make(map[string]string)

	}
	endpoint.Headers[os.ExpandEnv(name.GoString())] = os.ExpandEnv(hvalue.GoString())

	return starlark.None, nil
}

func (endpoint *Endpoint) setBasicAuth(thread *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (value starlark.Value, err error) {
	var (
		username, password starlark.String
	)
	if err := starlark.UnpackPositionalArgs("BasicAuth", args, kwargs, 2, &username, &password); err != nil {
		return nil, err
	}

	endpoint.BasicAuth = &map[string]string{
		"username": os.ExpandEnv(username.GoString()),
		"password": os.ExpandEnv(password.GoString()),
	}

	return starlark.None, nil
}

func (endpoint *Endpoint) setResponseType(thread *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (value starlark.Value, err error) {
	var (
		responseType starlark.String
	)
	if err := starlark.UnpackPositionalArgs("ResponseType", args, kwargs, 1, &responseType); err != nil {
		return nil, err
	}

	endpoint.ResponseType = responseType.GoString()

	return starlark.None, nil
}

func (endpoint *Endpoint) setTableDefinition(thread *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (value starlark.Value, err error) {
	var (
		tableDefinition *starlark.Dict
	)
	if err := starlark.UnpackPositionalArgs("TableDefinition", args, kwargs, 1, &tableDefinition); err != nil {
		return nil, err
	}

	tableDefinitionMap := make(map[string]string)
	for _, k := range tableDefinition.Keys() {
		v, _, err := tableDefinition.Get(k)
		if err != nil {
			return nil, err
		}
		tableDefinitionMap[k.(starlark.String).GoString()] = v.(starlark.String).GoString()
	}
	endpoint.TableDefinition = &tableDefinitionMap

	return starlark.None, nil
}

func (endpoint *Endpoint) setLoadStrategy(thread *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (value starlark.Value, err error) {
	var (
		strategy                     starlark.String
		primaryKey, ModifiedAtColumn starlark.String
		goBackHours                  starlark.Int
	)
	switch LoadStrategy(args[0].(starlark.String).GoString()) {
	case Full:
		if err := starlark.UnpackPositionalArgs("LoadStrategy", args, kwargs, 1, &strategy); err != nil {
			return nil, err
		}
	case ModifiedOnly:
		if err := starlark.UnpackArgs("LoadStrategy", args, kwargs, "strategy", &strategy, "primary_key", &primaryKey, "modified_at_column", &ModifiedAtColumn, "go_back_hours", &goBackHours); err != nil {
			return nil, err
		}
	case Incremental:
		if err := starlark.UnpackArgs("LoadStrategy", args, kwargs, "strategy", &strategy, "primary_key", &primaryKey); err != nil {
			return nil, err
		}
	default:
		return nil, errors.New("LoadStrategy(): invalid strategy, allowed values: Full, ModifiedOnly, Incremental")
	}

	goBackHoursInt, err := strconv.Atoi(goBackHours.String())
	if err != nil {
		return nil, fmt.Errorf("LoadStrategy(): go_back_hours error: %w", err)
	}
	endpoint.LoadOptions = LoadOptions{LoadStrategy(strategy), primaryKey.GoString(), ModifiedAtColumn.GoString(), goBackHoursInt}

	return starlark.None, nil
}

func (endpoint *Endpoint) setErrorHandling(thread *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (value starlark.Value, err error) {
	var (
		errorHandling *starlark.Dict
	)
	if err := starlark.UnpackPositionalArgs("ErrorHandling", args, kwargs, 1, &errorHandling); err != nil {
		return nil, err
	}

	errorHandlingMap := make(map[errorClass]ExitCode)
	for _, k := range errorHandling.Keys() {
		v, _, err := errorHandling.Get(k)
		if err != nil {
			return nil, err
		}
		if i, convErr := strconv.Atoi(v.String()); convErr != nil {
			return nil, fmt.Errorf("ErrorHandling value not supported: %s", v.String())
		} else {
			errorHandlingMap[errorClass(k.(starlark.String).GoString())] = ExitCode(i)
		}
	}
	endpoint.ErrorHandling = &errorHandlingMap

	return starlark.None, nil
}

func (endpoint *Endpoint) strategyOpts() (strategyOpts StrategyOptions) {
	strategyOpts.Strategy = string(endpoint.LoadOptions.Strategy)
	strategyOpts.PrimaryKey = endpoint.LoadOptions.PrimaryKey
	strategyOpts.ModifiedAtColumn = endpoint.LoadOptions.ModifiedAtColumn
	strategyOpts.HoursAgo = string(endpoint.LoadOptions.GoBackHours)
	return
}
