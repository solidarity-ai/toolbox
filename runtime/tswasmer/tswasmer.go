package tswasmer

import "encoding/json"

type Request struct {
	Binary string
	Args   []string
}

type Result struct {
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
	ExitCode int    `json:"exitCode"`
}

func Run(request Request) (Result, error) {
	payload := struct {
		Runtime string   `json:"runtime"`
		Binary  string   `json:"binary"`
		Args    []string `json:"args"`
	}{
		Runtime: "tswasmer-stub",
		Binary:  request.Binary,
		Args:    append([]string(nil), request.Args...),
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return Result{}, err
	}

	return Result{
		Stdout:   string(data),
		ExitCode: 0,
	}, nil
}
