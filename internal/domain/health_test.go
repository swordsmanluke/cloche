package domain_test

import (
	"testing"

	"github.com/cloche-dev/cloche/internal/domain"
	"github.com/stretchr/testify/assert"
)

func makeRuns(states ...domain.RunState) []domain.Run {
	runs := make([]domain.Run, len(states))
	for i, s := range states {
		runs[i] = domain.Run{ID: "r" + string(rune('0'+i)), State: s}
	}
	return runs
}

func TestCalculateHealth_Grey_NoRuns(t *testing.T) {
	result := domain.CalculateHealth(nil, 5)
	assert.Equal(t, domain.HealthGrey, result.Status)
	assert.Equal(t, 0, result.Passed)
	assert.Equal(t, 0, result.Failed)
	assert.Equal(t, 0, result.Total)
}

func TestCalculateHealth_Grey_EmptySlice(t *testing.T) {
	result := domain.CalculateHealth([]domain.Run{}, 5)
	assert.Equal(t, domain.HealthGrey, result.Status)
}

func TestCalculateHealth_Green_AllSucceeded(t *testing.T) {
	runs := makeRuns(
		domain.RunStateSucceeded,
		domain.RunStateSucceeded,
		domain.RunStateSucceeded,
	)
	result := domain.CalculateHealth(runs, 5)
	assert.Equal(t, domain.HealthGreen, result.Status)
	assert.Equal(t, 3, result.Passed)
	assert.Equal(t, 0, result.Failed)
	assert.Equal(t, 3, result.Total)
}

func TestCalculateHealth_Red_AllFailed(t *testing.T) {
	runs := makeRuns(
		domain.RunStateFailed,
		domain.RunStateFailed,
		domain.RunStateFailed,
	)
	result := domain.CalculateHealth(runs, 5)
	assert.Equal(t, domain.HealthRed, result.Status)
	assert.Equal(t, 0, result.Passed)
	assert.Equal(t, 3, result.Failed)
	assert.Equal(t, 3, result.Total)
}

func TestCalculateHealth_Red_AllCancelled(t *testing.T) {
	runs := makeRuns(
		domain.RunStateCancelled,
		domain.RunStateCancelled,
	)
	result := domain.CalculateHealth(runs, 5)
	assert.Equal(t, domain.HealthRed, result.Status)
	assert.Equal(t, 0, result.Passed)
	assert.Equal(t, 2, result.Failed)
	assert.Equal(t, 2, result.Total)
}

func TestCalculateHealth_Red_MixedFailedAndCancelled(t *testing.T) {
	runs := makeRuns(
		domain.RunStateFailed,
		domain.RunStateCancelled,
		domain.RunStateFailed,
	)
	result := domain.CalculateHealth(runs, 5)
	assert.Equal(t, domain.HealthRed, result.Status)
	assert.Equal(t, 0, result.Passed)
	assert.Equal(t, 3, result.Failed)
}

func TestCalculateHealth_Yellow_Mixed(t *testing.T) {
	runs := makeRuns(
		domain.RunStateSucceeded,
		domain.RunStateFailed,
		domain.RunStateSucceeded,
		domain.RunStateFailed,
		domain.RunStateSucceeded,
	)
	result := domain.CalculateHealth(runs, 5)
	assert.Equal(t, domain.HealthYellow, result.Status)
	assert.Equal(t, 3, result.Passed)
	assert.Equal(t, 2, result.Failed)
	assert.Equal(t, 5, result.Total)
}

func TestCalculateHealth_Blue_AllRunning(t *testing.T) {
	runs := makeRuns(
		domain.RunStateRunning,
		domain.RunStateRunning,
		domain.RunStatePending,
	)
	result := domain.CalculateHealth(runs, 5)
	assert.Equal(t, domain.HealthBlue, result.Status)
	assert.Equal(t, 0, result.Passed)
	assert.Equal(t, 0, result.Failed)
	assert.Equal(t, 3, result.Total)
}

func TestCalculateHealth_Blue_AllPending(t *testing.T) {
	runs := makeRuns(
		domain.RunStatePending,
		domain.RunStatePending,
	)
	result := domain.CalculateHealth(runs, 5)
	assert.Equal(t, domain.HealthBlue, result.Status)
}

func TestCalculateHealth_WindowSize_TruncatesRuns(t *testing.T) {
	// 5 runs total, window of 3 — only first 3 considered
	runs := makeRuns(
		domain.RunStateSucceeded,
		domain.RunStateSucceeded,
		domain.RunStateSucceeded,
		domain.RunStateFailed,
		domain.RunStateFailed,
	)
	result := domain.CalculateHealth(runs, 3)
	assert.Equal(t, domain.HealthGreen, result.Status)
	assert.Equal(t, 3, result.Passed)
	assert.Equal(t, 0, result.Failed)
	assert.Equal(t, 3, result.Total)
}

func TestCalculateHealth_WindowSize_LargerThanRuns(t *testing.T) {
	runs := makeRuns(
		domain.RunStateSucceeded,
		domain.RunStateFailed,
	)
	result := domain.CalculateHealth(runs, 10)
	assert.Equal(t, domain.HealthYellow, result.Status)
	assert.Equal(t, 1, result.Passed)
	assert.Equal(t, 1, result.Failed)
	assert.Equal(t, 2, result.Total)
}

func TestCalculateHealth_Green_WithInProgressRuns(t *testing.T) {
	// In-progress runs mixed with succeeded — should be green (all completed passed)
	runs := makeRuns(
		domain.RunStateRunning,
		domain.RunStateSucceeded,
		domain.RunStateSucceeded,
	)
	result := domain.CalculateHealth(runs, 5)
	assert.Equal(t, domain.HealthGreen, result.Status)
	assert.Equal(t, 2, result.Passed)
	assert.Equal(t, 0, result.Failed)
	assert.Equal(t, 3, result.Total)
}

func TestCalculateHealth_Yellow_InProgressWithMixed(t *testing.T) {
	runs := makeRuns(
		domain.RunStatePending,
		domain.RunStateSucceeded,
		domain.RunStateFailed,
	)
	result := domain.CalculateHealth(runs, 5)
	assert.Equal(t, domain.HealthYellow, result.Status)
	assert.Equal(t, 1, result.Passed)
	assert.Equal(t, 1, result.Failed)
	assert.Equal(t, 3, result.Total)
}

func TestCalculateHealth_Red_InProgressWithFailed(t *testing.T) {
	runs := makeRuns(
		domain.RunStateRunning,
		domain.RunStateFailed,
		domain.RunStateFailed,
	)
	result := domain.CalculateHealth(runs, 5)
	assert.Equal(t, domain.HealthRed, result.Status)
	assert.Equal(t, 0, result.Passed)
	assert.Equal(t, 2, result.Failed)
	assert.Equal(t, 3, result.Total)
}

func TestCalculateHealth_SingleRun_Succeeded(t *testing.T) {
	runs := makeRuns(domain.RunStateSucceeded)
	result := domain.CalculateHealth(runs, 5)
	assert.Equal(t, domain.HealthGreen, result.Status)
	assert.Equal(t, 1, result.Passed)
	assert.Equal(t, 0, result.Failed)
	assert.Equal(t, 1, result.Total)
}

func TestCalculateHealth_SingleRun_Failed(t *testing.T) {
	runs := makeRuns(domain.RunStateFailed)
	result := domain.CalculateHealth(runs, 5)
	assert.Equal(t, domain.HealthRed, result.Status)
	assert.Equal(t, 0, result.Passed)
	assert.Equal(t, 1, result.Failed)
	assert.Equal(t, 1, result.Total)
}

func TestCalculateHealth_WindowSize_One(t *testing.T) {
	runs := makeRuns(
		domain.RunStateSucceeded,
		domain.RunStateFailed,
		domain.RunStateFailed,
	)
	result := domain.CalculateHealth(runs, 1)
	assert.Equal(t, domain.HealthGreen, result.Status)
	assert.Equal(t, 1, result.Passed)
	assert.Equal(t, 0, result.Failed)
	assert.Equal(t, 1, result.Total)
}
