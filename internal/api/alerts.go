package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/worklydev/workly/internal/alert"
	"github.com/worklydev/workly/internal/store"
)

func alertChannelNotFound(id string) error {
	return &apiError{http.StatusNotFound, "alert_channel_not_found", fmt.Sprintf("alert channel %q does not exist", id)}
}

func toAlertChannel(c store.AlertChannel) AlertChannel {
	return AlertChannel{Id: c.ID, Type: AlertChannelType(c.Type), Target: alert.Masked(c), CreatedAt: c.CreatedAt}
}

func (h Handlers) ListAlertChannels(ctx context.Context, _ ListAlertChannelsRequestObject) (ListAlertChannelsResponseObject, error) {
	channels, err := h.Store.ListAlertChannels(ctx, workspaceID(ctx))
	if err != nil {
		return nil, err
	}
	out := make([]AlertChannel, len(channels))
	for i, c := range channels {
		out[i] = toAlertChannel(c)
	}
	return ListAlertChannels200JSONResponse{Channels: out, EmailEnabled: h.Alerts.Mailer != nil}, nil
}

func (h Handlers) CreateAlertChannel(ctx context.Context, req CreateAlertChannelRequestObject) (CreateAlertChannelResponseObject, error) {
	ws := workspaceID(ctx)
	typ := store.AlertChannelType(req.Body.Type)
	target, err := alert.ParseTarget(typ, req.Body.Target)
	if err != nil {
		return nil, invalid("%s", err)
	}
	if typ == store.AlertEmail {
		if h.Alerts.Mailer == nil {
			return nil, &apiError{http.StatusBadRequest, "email_unavailable",
				"this server isn't set up to send email (WORKLY_SMTP_URL); add a Slack channel instead"}
		}
		if err := h.Alerts.Mailer.CheckRecipient(ctx, ws, target); err != nil {
			return nil, err
		}
	}
	existing, err := h.Store.ListAlertChannels(ctx, ws)
	if err != nil {
		return nil, err
	}
	if len(existing) >= alert.MaxChannels {
		return nil, invalid("a workspace has at most %d alert channels", alert.MaxChannels)
	}
	c, err := h.Store.CreateAlertChannel(ctx, ws, typ, target)
	if errors.Is(err, store.ErrExists) {
		return nil, &apiError{http.StatusConflict, "alert_channel_exists", "alerts already go to " + alert.Masked(store.AlertChannel{Type: typ, Target: target})}
	}
	if err != nil {
		return nil, err
	}
	return CreateAlertChannel201JSONResponse(toAlertChannel(c)), nil
}

func (h Handlers) DeleteAlertChannel(ctx context.Context, req DeleteAlertChannelRequestObject) (DeleteAlertChannelResponseObject, error) {
	err := h.Store.DeleteAlertChannel(ctx, workspaceID(ctx), req.Id)
	if errors.Is(err, store.ErrNotFound) {
		return nil, alertChannelNotFound(req.Id)
	}
	if err != nil {
		return nil, err
	}
	return DeleteAlertChannel204Response{}, nil
}

func (h Handlers) TestAlertChannel(ctx context.Context, req TestAlertChannelRequestObject) (TestAlertChannelResponseObject, error) {
	ws := workspaceID(ctx)
	c, err := h.Store.GetAlertChannel(ctx, ws, req.Id)
	if errors.Is(err, store.ErrNotFound) {
		return nil, alertChannelNotFound(req.Id)
	}
	if err != nil {
		return nil, err
	}
	name, err := alert.WorkspaceName(ctx, h.Store, ws)
	if err != nil {
		return nil, err
	}
	if err := h.Alerts.Test(ctx, name, c); err != nil {
		return nil, &apiError{http.StatusBadGateway, "alert_failed", "the test alert wasn't sent: " + err.Error()}
	}
	return TestAlertChannel204Response{}, nil
}
