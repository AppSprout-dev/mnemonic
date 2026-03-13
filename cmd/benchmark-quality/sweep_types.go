package main

// SweepDefinition defines a set of parameter sweeps to run.
type SweepDefinition struct {
	Sweeps []ParamSweep `yaml:"sweeps"`
	Cycles int          `yaml:"cycles"` // 0 means use CLI default
}

// ParamSweep defines a single parameter and its values to test.
type ParamSweep struct {
	Param  string    `yaml:"param"`
	Values []float64 `yaml:"values"`
}

// SweepResult holds the scores for one parameter value.
type SweepResult struct {
	Param     string
	Value     float64
	Scores    aggregateResult
	Delta     map[string]float64 // metric name -> delta from baseline
	IsDefault bool               // true if this value is the default
}

// SweepReport holds the full sweep comparison.
type SweepReport struct {
	Baseline     aggregateResult
	ParamResults map[string][]SweepResult // param name -> results for each value
}
