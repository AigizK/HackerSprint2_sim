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
	case errors.Is(err, ErrControlUnauthorized):
		return APIError{Status: 401, Code: "CONTROL_UNAUTHORIZED", Message: err.Error()}
	case errors.Is(err, ErrTargetUnauthorized):
		return APIError{Status: 403, Code: "TARGET_UNAUTHORIZED", Message: err.Error()}
	case errors.Is(err, ErrCredentialsExpired):
		return APIError{Status: 403, Code: "CREDENTIALS_EXPIRED", Message: err.Error()}
	case errors.Is(err, simulation.ErrRunNotFound):
		return APIError{Status: 404, Code: "RUN_NOT_FOUND", Message: "Run does not exist"}
	case errors.Is(err, ErrManualWorldNotFound):
		return APIError{Status: 404, Code: "MANUAL_WORLD_NOT_FOUND", Message: "Manual world does not exist"}
	case errors.Is(err, simulation.ErrProductNotFound):
		return APIError{Status: 404, Code: "PRODUCT_NOT_FOUND", Message: err.Error()}
	case errors.Is(err, simulation.ErrProjectionNotFound):
		return APIError{Status: 404, Code: "OPERATION_NOT_FOUND", Message: err.Error()}
	case errors.Is(err, simulation.ErrResourceNotFound):
		return APIError{Status: 404, Code: "RESOURCE_NOT_FOUND", Message: err.Error()}
	case errors.Is(err, simulation.ErrRunCompleted):
		return APIError{Status: 409, Code: "RUN_COMPLETED", Message: err.Error()}
	case errors.Is(err, ErrConcurrentRunRequest):
		return APIError{Status: 409, Code: "CONCURRENT_RUN_REQUEST", Message: err.Error()}
	case errors.Is(err, simulation.ErrVersionConflict):
		return APIError{Status: 409, Code: "CONCURRENT_RUN_REQUEST", Message: err.Error()}
	case errors.Is(err, ErrIdempotencyConflict), errors.Is(err, simulation.ErrIdempotencyConflict):
		return APIError{Status: 409, Code: "IDEMPOTENCY_CONFLICT", Message: err.Error()}
	case errors.Is(err, simulation.ErrSiteNotStopped):
		return APIError{Status: 409, Code: "SITE_NOT_STOPPED", Message: err.Error()}
	case errors.Is(err, simulation.ErrSiteNotRunning):
		return APIError{Status: 409, Code: "SITE_NOT_RUNNING", Message: err.Error()}
	case errors.Is(err, simulation.ErrDatabaseNotEmpty):
		return APIError{Status: 409, Code: "DB_NOT_EMPTY", Message: err.Error()}
	case errors.Is(err, simulation.ErrBackupNotReady):
		return APIError{Status: 409, Code: "BACKUP_NOT_READY", Message: err.Error()}
	case errors.Is(err, simulation.ErrInsufficientDisk):
		return APIError{Status: 409, Code: "INSUFFICIENT_DISK_SPACE", Message: err.Error()}
	case errors.Is(err, simulation.ErrDatabaseNotReady):
		return APIError{Status: 409, Code: "DB_NOT_READY", Message: err.Error()}
	case errors.Is(err, simulation.ErrDatabaseBackupStale):
		return APIError{Status: 409, Code: "DB_BACKUP_STALE", Message: err.Error()}
	case errors.Is(err, simulation.ErrSiteConfigConflict):
		return APIError{Status: 409, Code: "SITE_CONFIG_CONFLICT", Message: err.Error()}
	case errors.Is(err, simulation.ErrLastBackendRequired):
		return APIError{Status: 409, Code: "LAST_BACKEND_REQUIRED", Message: err.Error()}
	case errors.Is(err, simulation.ErrServerInUse):
		return APIError{Status: 409, Code: "SERVER_IN_USE", Message: err.Error()}
	case errors.Is(err, simulation.ErrBackendUnavailable):
		return APIError{Status: 409, Code: "BACKEND_UNAVAILABLE", Message: err.Error()}
	case errors.Is(err, simulation.ErrOperationInProgress):
		return APIError{Status: 409, Code: "OPERATION_IN_PROGRESS", Message: err.Error()}
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
