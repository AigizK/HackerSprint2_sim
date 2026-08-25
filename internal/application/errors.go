package application

import (
	"errors"

	"github.com/aigizk/hackersprint2-sim/internal/simulation"
)

type APIError struct {
	Status  int
	Code    string
	Message string
}

func ClassifyError(err error) APIError {
	switch {
	case err == nil:
		return APIError{}
	case errors.Is(err, ErrUnauthorized):
		return APIError{Status: 401, Code: "UNAUTHORIZED", Message: "Agent API key or ownership is invalid"}
	case errors.Is(err, simulation.ErrRunNotFound):
		return APIError{Status: 404, Code: "RUN_NOT_FOUND", Message: "Run does not exist"}
	case errors.Is(err, ErrManualWorldNotFound):
		return APIError{Status: 404, Code: "MANUAL_WORLD_NOT_FOUND", Message: "Manual world does not exist"}
	case errors.Is(err, simulation.ErrProductNotFound):
		return APIError{Status: 404, Code: "PRODUCT_NOT_FOUND", Message: err.Error()}
	case errors.Is(err, simulation.ErrDeploymentNotFound):
		return APIError{Status: 404, Code: "DEPLOYMENT_NOT_FOUND", Message: err.Error()}
	case errors.Is(err, simulation.ErrProjectionNotFound):
		return APIError{Status: 404, Code: "OPERATION_NOT_FOUND", Message: err.Error()}
	case errors.Is(err, simulation.ErrDeploymentLocked):
		return APIError{Status: 409, Code: "DEPLOYMENT_ORDER_VIOLATION", Message: err.Error()}
	case errors.Is(err, simulation.ErrDeploymentInProgress):
		return APIError{Status: 409, Code: "DEPLOYMENT_IN_PROGRESS", Message: err.Error()}
	case errors.Is(err, simulation.ErrDeploymentApplied):
		return APIError{Status: 409, Code: "DEPLOYMENT_ALREADY_APPLIED", Message: err.Error()}
	case errors.Is(err, simulation.ErrRunCompleted):
		return APIError{Status: 409, Code: "RUN_COMPLETED", Message: err.Error()}
	case errors.Is(err, ErrConcurrentRunRequest):
		return APIError{Status: 409, Code: "CONCURRENT_RUN_REQUEST", Message: err.Error()}
	case errors.Is(err, simulation.ErrVersionConflict):
		return APIError{Status: 409, Code: "CONCURRENT_RUN_REQUEST", Message: err.Error()}
	case errors.Is(err, ErrIdempotencyConflict), errors.Is(err, simulation.ErrIdempotencyConflict):
		return APIError{Status: 409, Code: "IDEMPOTENCY_CONFLICT", Message: err.Error()}
	case errors.Is(err, ErrInvalidSeed):
		return APIError{Status: 400, Code: "INVALID_SEED", Message: ErrInvalidSeed.Error()}
	case errors.Is(err, ErrMinimumAdvance):
		return APIError{Status: 400, Code: "MINIMUM_ADVANCE_IS_300_SECONDS", Message: ErrMinimumAdvance.Error()}
	case errors.Is(err, ErrInvalidRequest), errors.Is(err, simulation.ErrInvalidCommand):
		return APIError{Status: 400, Code: "INVALID_REQUEST", Message: err.Error()}
	default:
		return APIError{Status: 500, Code: "INTERNAL_ERROR", Message: "Internal simulator error"}
	}
}
