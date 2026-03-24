package api

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/draftea/sr-backend-draftea-challenge/internal/platform/httpx"
	"github.com/draftea/sr-backend-draftea-challenge/internal/platform/messaging"
	"github.com/draftea/sr-backend-draftea-challenge/internal/services/saga/activities"
	"github.com/draftea/sr-backend-draftea-challenge/internal/services/saga/client"
	"github.com/draftea/sr-backend-draftea-challenge/internal/services/saga/repository"
	"github.com/draftea/sr-backend-draftea-challenge/internal/services/saga/usecases/commanderror"
	"github.com/draftea/sr-backend-draftea-challenge/internal/services/saga/usecases/idempotency"
	"github.com/draftea/sr-backend-draftea-challenge/internal/services/saga/usecases/startdeposit"
	"github.com/draftea/sr-backend-draftea-challenge/internal/services/saga/usecases/startpurchase"
	"github.com/draftea/sr-backend-draftea-challenge/internal/services/saga/usecases/startrefund"
)

// DefaultSagaTimeout is the default duration before a saga times out.
const DefaultSagaTimeout = 30 * time.Second

// Handler provides HTTP handlers for the saga-orchestrator command ingress.
type Handler struct {
	startDeposit                *startdeposit.UseCase
	startPurchase               *startpurchase.UseCase
	startRefund                 *startrefund.UseCase
	mockProviderControlsEnabled bool
}

const (
	headerMockProviderDelay  = "X-Mock-Provider-Delay"
	headerMockProviderResult = "X-Mock-Provider-Result"
)

// NewHandler creates a new Handler.
func NewHandler(
	repo repository.Repository,
	catalogClient client.CatalogClient,
	paymentsClient client.PaymentsClient,
	publisher activities.Publisher,
	sagaTimeout time.Duration,
	logger *slog.Logger,
	enableMockProviderControls bool,
) *Handler {
	if sagaTimeout == 0 {
		sagaTimeout = DefaultSagaTimeout
	}

	idempotencyService := idempotency.NewService(repo)

	return &Handler{
		startDeposit: startdeposit.New(repo, paymentsClient, publisher, idempotencyService, sagaTimeout),
		startPurchase: startpurchase.New(
			repo,
			catalogClient,
			paymentsClient,
			publisher,
			idempotencyService,
			sagaTimeout,
		),
		startRefund: startrefund.New(
			repo,
			catalogClient,
			paymentsClient,
			publisher,
			idempotencyService,
			sagaTimeout,
		),
		mockProviderControlsEnabled: enableMockProviderControls,
	}
}

// HandleDeposit handles POST /deposits.
func (h *Handler) HandleDeposit(w http.ResponseWriter, r *http.Request) {
	var cmd DepositCommand
	if err := httpx.Decode(r, &cmd); err != nil {
		httpx.WriteDecodeError(w, err)
		return
	}

	if fields := validateDepositCommand(&cmd); len(fields) > 0 {
		httpx.ValidationError(w, fields)
		return
	}
	if cmd.Currency == "" {
		cmd.Currency = "ARS"
	}
	mockProvider, headerErr := parseMockProviderControls(r, h.mockProviderControlsEnabled)
	if headerErr != nil {
		httpx.ErrorWithCode(w, headerErr.status, headerErr.message, headerErr.code)
		return
	}

	result, err := h.startDeposit.Execute(r.Context(), startdeposit.Command{
		UserID:         cmd.UserID,
		Amount:         cmd.Amount,
		Currency:       cmd.Currency,
		IdempotencyKey: cmd.IdempotencyKey,
		MockProvider:   mockProvider,
	})
	if writeCommandError(w, err) {
		return
	}
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal server error")
		return
	}
	if result.Cached != nil {
		writeCachedResponse(w, result.Cached)
		return
	}

	httpx.JSON(w, http.StatusAccepted, CommandAcceptedResponse{
		TransactionID: result.Response.TransactionID,
		Status:        result.Response.Status,
	})
}

// HandlePurchase handles POST /purchases.
func (h *Handler) HandlePurchase(w http.ResponseWriter, r *http.Request) {
	var cmd PurchaseCommand
	if err := httpx.Decode(r, &cmd); err != nil {
		httpx.WriteDecodeError(w, err)
		return
	}

	if fields := validatePurchaseCommand(&cmd); len(fields) > 0 {
		httpx.ValidationError(w, fields)
		return
	}

	result, err := h.startPurchase.Execute(r.Context(), startpurchase.Command{
		UserID:         cmd.UserID,
		OfferingID:     cmd.OfferingID,
		IdempotencyKey: cmd.IdempotencyKey,
	})
	if writeCommandError(w, err) {
		return
	}
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal server error")
		return
	}
	if result.Cached != nil {
		writeCachedResponse(w, result.Cached)
		return
	}

	httpx.JSON(w, http.StatusAccepted, CommandAcceptedResponse{
		TransactionID: result.Response.TransactionID,
		Status:        result.Response.Status,
	})
}

// HandleRefund handles POST /refunds.
func (h *Handler) HandleRefund(w http.ResponseWriter, r *http.Request) {
	var cmd RefundCommand
	if err := httpx.Decode(r, &cmd); err != nil {
		httpx.WriteDecodeError(w, err)
		return
	}

	if fields := validateRefundCommand(&cmd); len(fields) > 0 {
		httpx.ValidationError(w, fields)
		return
	}

	result, err := h.startRefund.Execute(r.Context(), startrefund.Command{
		UserID:         cmd.UserID,
		OfferingID:     cmd.OfferingID,
		TransactionID:  cmd.TransactionID,
		IdempotencyKey: cmd.IdempotencyKey,
	})
	if writeCommandError(w, err) {
		return
	}
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal server error")
		return
	}
	if result.Cached != nil {
		writeCachedResponse(w, result.Cached)
		return
	}

	httpx.JSON(w, http.StatusAccepted, CommandAcceptedResponse{
		TransactionID: result.Response.TransactionID,
		Status:        result.Response.Status,
	})
}

func writeCommandError(w http.ResponseWriter, err error) bool {
	if err == nil {
		return false
	}

	var commandErr *commanderror.Error
	if !errors.As(err, &commandErr) {
		return false
	}

	if commandErr.HTTPCode() == "" {
		httpx.Error(w, commandErr.HTTPStatus(), commandErr.HTTPMessage())
		return true
	}

	httpx.ErrorWithCode(w, commandErr.HTTPStatus(), commandErr.HTTPMessage(), commandErr.HTTPCode())
	return true
}

func writeCachedResponse(w http.ResponseWriter, replay *idempotency.Replay) {
	httpx.WriteJSONBytes(w, replay.StatusCode, replay.Body)
}

// validateDepositCommand returns field-level validation errors for a deposit command.
func validateDepositCommand(cmd *DepositCommand) []httpx.FieldError {
	var fields []httpx.FieldError
	if cmd.UserID == "" {
		fields = append(fields, httpx.FieldError{Field: "user_id", Code: "required"})
	}
	if cmd.Amount <= 0 {
		fields = append(fields, httpx.FieldError{Field: "amount", Code: "must_be_positive"})
	}
	if cmd.IdempotencyKey == "" {
		fields = append(fields, httpx.FieldError{Field: "idempotency_key", Code: "required"})
	}
	return fields
}

type mockProviderHeaderError struct {
	status  int
	code    string
	message string
}

func parseMockProviderControls(r *http.Request, enabled bool) (*messaging.MockProviderControls, *mockProviderHeaderError) {
	rawDelay := strings.TrimSpace(r.Header.Get(headerMockProviderDelay))
	rawResult := strings.ToLower(strings.TrimSpace(r.Header.Get(headerMockProviderResult)))
	if rawDelay == "" && rawResult == "" {
		return nil, nil
	}
	if !enabled {
		return nil, &mockProviderHeaderError{
			status:  http.StatusBadRequest,
			code:    "MOCK_PROVIDER_CONTROLS_DISABLED",
			message: "mock provider controls are disabled",
		}
	}

	controls := &messaging.MockProviderControls{}
	if rawDelay != "" {
		delay, err := time.ParseDuration(rawDelay)
		if err != nil || delay < 0 {
			return nil, &mockProviderHeaderError{
				status:  http.StatusBadRequest,
				code:    "INVALID_MOCK_PROVIDER_DELAY",
				message: "X-Mock-Provider-Delay must be a valid non-negative duration",
			}
		}
		controls.Delay = delay.String()
	}

	if rawResult != "" {
		switch rawResult {
		case "success", "fail":
			controls.Result = rawResult
		default:
			return nil, &mockProviderHeaderError{
				status:  http.StatusBadRequest,
				code:    "INVALID_MOCK_PROVIDER_RESULT",
				message: "X-Mock-Provider-Result must be either success or fail",
			}
		}
	}

	return controls, nil
}

// validatePurchaseCommand returns field-level validation errors for a purchase command.
func validatePurchaseCommand(cmd *PurchaseCommand) []httpx.FieldError {
	var fields []httpx.FieldError
	if cmd.UserID == "" {
		fields = append(fields, httpx.FieldError{Field: "user_id", Code: "required"})
	}
	if cmd.OfferingID == "" {
		fields = append(fields, httpx.FieldError{Field: "offering_id", Code: "required"})
	}
	if cmd.IdempotencyKey == "" {
		fields = append(fields, httpx.FieldError{Field: "idempotency_key", Code: "required"})
	}
	return fields
}

// validateRefundCommand returns field-level validation errors for a refund command.
func validateRefundCommand(cmd *RefundCommand) []httpx.FieldError {
	var fields []httpx.FieldError
	if cmd.UserID == "" {
		fields = append(fields, httpx.FieldError{Field: "user_id", Code: "required"})
	}
	if cmd.OfferingID == "" {
		fields = append(fields, httpx.FieldError{Field: "offering_id", Code: "required"})
	}
	if cmd.TransactionID == "" {
		fields = append(fields, httpx.FieldError{Field: "transaction_id", Code: "required"})
	}
	if cmd.IdempotencyKey == "" {
		fields = append(fields, httpx.FieldError{Field: "idempotency_key", Code: "required"})
	}
	return fields
}
