package vision

import "context"

// Fake is a deterministic Provider for tests and demos.
type Fake struct {
	// Result is returned for every call unless Fn is set.
	Result DetectResult
	Err    error
	Fn     func(DetectRequest) (DetectResult, error)
	Calls  int
}

func (f *Fake) Name() string { return "fake" }

func (f *Fake) Detect(_ context.Context, req DetectRequest) (DetectResult, error) {
	f.Calls++
	if f.Fn != nil {
		return f.Fn(req)
	}
	return f.Result, f.Err
}
