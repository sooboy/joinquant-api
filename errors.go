package joinquant

import (
	"errors"
	"fmt"
)

var (
	// ErrSessionExpired means JoinQuant redirected the request to the login page
	// or explicitly reported that the current session is not logged in.
	ErrSessionExpired         = errors.New("joinquant session expired")
	ErrSessionNotFound        = errors.New("joinquant persisted session not found")
	ErrSessionPersistence     = errors.New("joinquant session persistence failed")
	ErrCredentialsUnavailable = errors.New("joinquant credentials unavailable")
	ErrInvalidResponse        = errors.New("invalid joinquant response")
	// ErrTruncated means the web endpoint reported that the returned collection
	// is incomplete. A copy engine must not treat omitted positions as zero.
	ErrTruncated        = errors.New("joinquant response is truncated")
	ErrBacktestNotReady = errors.New("joinquant backtest data is not ready")
	// ErrSubmissionUnknown means a write may have reached JoinQuant. Do not retry
	// automatically; reconcile with the task list before submitting again.
	ErrSubmissionUnknown = errors.New("joinquant submission outcome is unknown")
)

// APIError is an error returned by JoinQuant's JSON envelope.
type APIError struct {
	HTTPStatus int
	Status     string
	Code       string
	Message    string
}

func (e *APIError) Error() string {
	if e == nil {
		return "<nil>"
	}
	if e.Message != "" {
		return fmt.Sprintf("joinquant API error: code=%s status=%s: %s", e.Code, e.Status, e.Message)
	}
	return fmt.Sprintf("joinquant API error: code=%s status=%s http=%d", e.Code, e.Status, e.HTTPStatus)
}
