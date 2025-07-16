package autosize

import (
	"errors"
	"fmt"
	"log"
	"math"

	"gonum.org/v1/gonum/optimize"

	"github.com/openshift/cluster-node-tuning-operator/pkg/performanceprofile/profilecreator"
)

// Assumptions:
// 1. All the machines in the node pool have identical HW specs and need identical sizing.
// 2. We cannot distinguish between infra/OS CPU requirements and control plane CPU requirement.
//    We will conflate the two costs in the latter.
//
// Definitions:
// x_c: CPUs for the control plane - includes x_i: CPUs for OS/Infra
// x_w: CPUs for the workload
// Tc: Total available CPUs (includes OS/Infra
//
// Hard Constraints:
//   x_c, x_w are integers because we need to dedicate full cores
//   x_c, x_w >= 0
//   x_c + x_w <= Tc
//   x_c >= req(x_w) // control plane and infra cost is a function of the expected workload
//
// Objective:
// We want to maximize x_w, or, equivalently, minimize x_c

const (
	defaultPenaltyWeight                 float64 = 100.0
	defaultReservedRatioInitial          float64 = 0.0625 // 1/16. determined empirically. Use only as initial value.
	defaultReservedRatioMax              float64 = 0.25   // 1/4. determined empirically. This is the practical upper bound.
	defaultControlPlaneWorkloadCoreRatio float64 = 0.075  // TODO: how much control plane/infra power do we need to support the workload?
)

var (
	ErrUnderallocatedControlPlane = errors.New("not enough CPUs for control plane")
	ErrOverallocatedControlPlane  = errors.New("too many CPUs for control plane")
	ErrInconsistentAllocation     = errors.New("inconsistent CPus allocation")
)

type Env struct {
	Log *log.Logger
}

func DefaultEnv() Env {
	return Env{
		Log: profilecreator.GetAlertSink(),
	}
}

type Params struct {
	OfflinedCPUCount    int
	UserLevelNetworking bool
	MachineData         *profilecreator.GHWHandler
	// cached vars
	totalCPUs int
	smtLevel  int
}

func (p Params) String() string {
	return fmt.Sprintf("cpus=%d offline=%v SMTLevel=%v", p.totalCPUs, p.OfflinedCPUCount, p.smtLevel)
}

func setupMachineData(p *Params) error {
	var err error

	cpus, err := p.MachineData.CPU()
	if err != nil {
		return err
	}

	p.totalCPUs = int(cpus.TotalHardwareThreads)
	// NOTE: this assumes all cores are equal, but it's a limitation also shared by GHW. CPUs with P/E cores will be misrepresented.
	p.smtLevel = int(cpus.TotalHardwareThreads / cpus.TotalCores)
	return nil
}

func (p Params) TotalCPUs() int {
	return p.totalCPUs
}

func (p Params) SMTLevel() int {
	return p.smtLevel
}

func (p Params) DefaultControlPlaneCores() int {
	// intentionally overallocate to have a safe baseline
	Tc := p.totalCPUs
	return int(math.Round(float64(Tc) * defaultReservedRatioInitial)) // TODO handle SMT
}

// Get x_c, x_w as initial hardcoded value. Subject to optimization
func (p Params) DefaultAllocation() Values {
	Tc := p.totalCPUs
	x_c := p.DefaultControlPlaneCores()
	return Values{
		ReservedCPUCount: x_c,
		IsolatedCPUCount: Tc - x_c,
	}
}

func (p Params) initialValue() []float64 {
	vals := p.DefaultAllocation()
	return []float64{
		float64(vals.ReservedCPUCount), // x_c
		float64(vals.IsolatedCPUCount), // x_w
	}
}

func (p Params) controlPlaneRequirement(x_w float64) float64 {
	R := defaultControlPlaneWorkloadCoreRatio
	if p.UserLevelNetworking {
		R = 0.0
	}
	// TODO: the most obvious relationship is for kernel level networking.
	// We start with a linear relationship because its simplicity.
	return float64(p.DefaultControlPlaneCores()) + R*x_w
}

type Score struct {
	Cost float64 // the lower the better
}

func (sc Score) String() string {
	val := -sc.Cost // positive values are easier to grasp
	return fmt.Sprintf("optimization result: %.3f (higher is better)", val)
}

type Values struct {
	// we intentionally compute the recommended cpu count, not precise allocation, because
	// this is better done by other packages. We may expose the precise allocation as hint
	// or for reference purposes in the future
	ReservedCPUCount int
	IsolatedCPUCount int
}

func (vals Values) String() string {
	return fmt.Sprintf("reserved=%v/isolated=%v", vals.ReservedCPUCount, vals.IsolatedCPUCount)
}

// gonum doesn't support bounds yet so we have to make this an explicit step
// https://github.com/gonum/gonum/issues/1725
func Validate(params Params, vals Values) error {
	Tc := params.TotalCPUs()
	if vals.ReservedCPUCount < 1 { // TODO handle SMT
		return ErrUnderallocatedControlPlane
	}
	if vals.ReservedCPUCount > int(math.Round((float64(Tc) * defaultReservedRatioMax))) { // works, but likely unacceptable
		return ErrOverallocatedControlPlane
	}
	if Tc != vals.ReservedCPUCount+vals.IsolatedCPUCount {
		return ErrInconsistentAllocation
	}
	return nil
}

// Objective function to minimize.
// x[0] is x_c
// x[1] is x_w
func objective(p Params, x []float64) float64 {
	x_c := x[0]
	x_w := x[1]

	// Our original objective is to maximize x_w, so we minimize -x_w
	target := -x_w

	// gonum doesn't support bounds yet so we have to use penalties:
	// https://github.com/gonum/gonum/issues/1725

	// Hard Constraints
	var hardPenalty float64
	// Don't exceed total CPUs
	hardPenalty += defaultPenaltyWeight * math.Pow(math.Max(0, x_c+x_w-float64(p.TotalCPUs())), 2)

	// Meet the control plane/infra requirement to avoid the workload to starve
	hardPenalty += defaultPenaltyWeight * math.Pow(math.Max(0, p.controlPlaneRequirement(x_w)-x_c), 2)

	// Must use positive CPU values (since gonum/optimize doesn't have simple bounds for all solvers)
	hardPenalty += defaultPenaltyWeight*math.Pow(math.Max(0, -x_c), 2) + math.Pow(math.Max(0, -x_w), 2)

	// Allocate in multiples of SMT level (usually 2) -- TODO: should be soft?
	hardPenalty += defaultPenaltyWeight * math.Pow(math.Max(0, -float64(int(math.Round(x_c))%p.SMTLevel())), 2)

	return target + hardPenalty
}

func Compute(env Env, params Params) (Values, Score, error) {
	err := setupMachineData(&params)
	if err != nil {
		env.Log.Printf("Optimization failed: %v", err)
		return params.DefaultAllocation(), Score{}, err
	}

	problem := optimize.Problem{
		Func: func(x []float64) float64 {
			return objective(params, x)
		},
	}

	settings := &optimize.Settings{
		MajorIterations: 99,
	}

	env.Log.Printf("Optimization start. Default allocation: %v", params.DefaultAllocation().String())
	env.Log.Printf("Optimization start. Params: %v", params.String())

	result, err := optimize.Minimize(problem, params.initialValue(), settings, &optimize.NelderMead{})
	if err != nil {
		env.Log.Printf("Optimization failed: %v", err)
		return params.DefaultAllocation(), Score{}, err
	}

	totCPUs := params.TotalCPUs()
	score := Score{Cost: result.F}
	x_c := int(math.Round(result.Location.X[0]))

	opt := Values{
		ReservedCPUCount: x_c,
		IsolatedCPUCount: totCPUs - x_c, // we can use x_w, but we just leverage invariants
	}
	env.Log.Printf("Optimization result: %s", opt.String())

	if err := Validate(params, opt); err != nil {
		env.Log.Printf("Optimization invalid: %v", err)
		return params.DefaultAllocation(), Score{}, err
	}

	// postprocessing must be done after successfull validation
	vals := postProcess(params, opt)
	env.Log.Printf("Optimization postprocess. %s => %s", opt.String(), vals.String())

	env.Log.Printf("Optimization done. Score: %v %s totalCPUs=%d", score.String(), vals.String(), totCPUs)
	return vals, score, nil
}

func postProcess(params Params, vals Values) Values {
	Tc := params.TotalCPUs()
	sl := params.SMTLevel()
	x_c := asMultipleOf(vals.ReservedCPUCount, sl)
	ret := Values{
		ReservedCPUCount: x_c,
		IsolatedCPUCount: Tc - x_c,
	}
	return ret
}

func asMultipleOf(v, x int) int {
	r := v % x
	if r == 0 {
		return v
	}
	return v + r
}
