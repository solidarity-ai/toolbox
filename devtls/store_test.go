package devtls

import (
	"context"
	"errors"
	"os"
	"strings"
)

type runnerCall struct {
	name string
	args []string
}

type fakeRunner struct {
	paths              map[string]string
	calls              []runnerCall
	err                error
	privateKeyObserved bool
}

func (r *fakeRunner) LookPath(file string) (string, error) {
	if path := r.paths[file]; path != "" {
		return path, nil
	}
	return "", errors.New("not found")
}

func (r *fakeRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	r.calls = append(r.calls, runnerCall{name: name, args: append([]string(nil), args...)})
	for _, arg := range args {
		if data, err := os.ReadFile(arg); err == nil && strings.Contains(string(data), "PRIVATE KEY") {
			r.privateKeyObserved = true
		}
	}
	return nil, r.err
}
