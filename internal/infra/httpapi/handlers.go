package httpapi

import (
	"net/http"
	"strconv"

	"jungle-gaming-challeng/internal/app"
	"jungle-gaming-challeng/internal/domain/failure"
	"jungle-gaming-challeng/internal/domain/ids"
)

const (
	headerIdempotencyKey = "Idempotency-Key"
	statusAlive          = "alive"
	statusReady          = "ready"
	statusUnavailable    = "unavailable"
)

func (a *API) openWallet(w http.ResponseWriter, r *http.Request) {
	var request openWalletRequest
	if err := a.decode(w, r, &request); err != nil {
		a.writeError(w, r, err)
		return
	}
	playerID, err := app.ParseRequired(ids.ParsePlayerID, request.PlayerID)
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	if request.InitialBalance == nil {
		a.writeError(w, r, invalidInput(failure.MalformedRequest))
		return
	}

	opened, err := a.deps.Service.OpenWallet(r.Context(), app.OpenWalletInput{
		PlayerID:       playerID,
		InitialBalance: *request.InitialBalance,
		CorrelationID:  correlationID(r.Context()),
	})
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, toWalletResponse(opened))
}

func (a *API) getWallet(w http.ResponseWriter, r *http.Request) {
	walletID, err := app.ParseRequired(ids.ParseWalletID, r.PathValue("walletId"))
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	state, err := a.deps.Service.GetWallet(r.Context(), walletID)
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toWalletResponse(state))
}

func (a *API) listLedger(w http.ResponseWriter, r *http.Request) {
	walletID, err := app.ParseRequired(ids.ParseWalletID, r.PathValue("walletId"))
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	limit := app.DefaultLedgerPageSize
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if limit, err = strconv.Atoi(raw); err != nil {
			a.writeError(w, r, invalidInput(failure.MalformedRequest))
			return
		}
	}

	page, err := a.deps.Service.ListLedger(r.Context(), walletID, r.URL.Query().Get("cursor"), limit)
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toLedgerResponse(page))
}

func (a *API) reconcileWallet(w http.ResponseWriter, r *http.Request) {
	walletID, err := app.ParseRequired(ids.ParseWalletID, r.PathValue("walletId"))
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	result, err := a.deps.Service.ReconcileWallet(r.Context(), walletID)
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toReconciliationResponse(result))
}

func (a *API) submitTransaction(w http.ResponseWriter, r *http.Request) {
	var request submitRequest
	if err := a.decode(w, r, &request); err != nil {
		a.writeError(w, r, err)
		return
	}
	in, err := request.toInput(r.Header.Get(headerIdempotencyKey))
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	if !callerFrom(r.Context()).CanSubmitAs(in.External.ProviderID) {
		a.writeError(w, r, app.ErrForbidden)
		return
	}
	in.CorrelationID = correlationID(r.Context())

	result, err := a.deps.Service.SubmitTransaction(r.Context(), in)
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	writeJSON(w, submitStatus(result.Transaction.Status), toSubmitResponse(result))
}

func (a *API) getTransaction(w http.ResponseWriter, r *http.Request) {
	transactionID, err := app.ParseRequired(ids.ParseTransactionID, r.PathValue("transactionId"))
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	state, err := a.deps.Service.GetTransaction(r.Context(), callerFrom(r.Context()), transactionID)
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toTransactionResponse(state))
}

func (a *API) getProviderTransaction(w http.ResponseWriter, r *http.Request) {
	providerID, err := app.ParseRequired(ids.ParseProviderID, r.PathValue("providerId"))
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	externalID, err := app.ParseRequired(ids.ParseExternalTransactionID, r.PathValue("externalTransactionId"))
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	caller := callerFrom(r.Context())
	if !caller.CanReadProvider(providerID) {
		a.writeError(w, r, app.ErrForbidden)
		return
	}
	state, err := a.deps.Service.GetProviderTransaction(r.Context(), caller, providerID, externalID)
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toTransactionResponse(state))
}

func (a *API) live(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, readinessResponse{Status: statusAlive})
}

func (a *API) ready(w http.ResponseWriter, r *http.Request) {
	if failed := a.deps.Readiness.NotReady(r.Context()); len(failed) > 0 {
		writeJSON(w, http.StatusServiceUnavailable, readinessResponse{Status: statusUnavailable, Failed: failed})
		return
	}
	writeJSON(w, http.StatusOK, readinessResponse{Status: statusReady})
}
