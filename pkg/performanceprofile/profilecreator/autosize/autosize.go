package autosize

type Params struct {
	OfflinedCPUCount int
}

type Values struct {
	ReservedCPUCount int
}

type Env struct{}

func DefaultEnv() Env {
	return Env{}
}

type Score struct{}

func Compute(env Env, params Params) (Values, Score, error) {
	return Values{}, Score{}, nil
}
