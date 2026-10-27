package handler_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	authv1 "github.com/AlexandrKhromov2005/vault-chat/api/gen/go/auth/v1"
	"github.com/AlexandrKhromov2005/vault-chat/internal/gateway/handler"
	"github.com/AlexandrKhromov2005/vault-chat/internal/gateway/middleware"
)

func emailStatus(t *testing.T, code codes.Code, reason, domain string) error {
	t.Helper()
	s, err := status.New(code, "fixed safe message").WithDetails(&errdetails.ErrorInfo{Domain: domain, Reason: reason})
	require.NoError(t, err)
	return s.Err()
}

type emailEndpointCase struct {
	name           string
	handle         func(*handler.AuthHandler) http.HandlerFunc
	body           string
	request, reply proto.Message
	status         int
}

func emailEndpoints() []emailEndpointCase {
	return []emailEndpointCase{
		{"RequestEmailVerification", func(h *handler.AuthHandler) http.HandlerFunc { return h.RequestEmailVerification },
			`{"email":"alice@example.invalid","password":"FreshPassword12"}`, &authv1.RequestEmailVerificationRequest{Email: "alice@example.invalid", Password: "FreshPassword12"}, &authv1.RequestEmailVerificationResponse{Message: "private backend state"}, http.StatusAccepted},
		{"ConfirmEmailVerification", func(h *handler.AuthHandler) http.HandlerFunc { return h.ConfirmEmailVerification },
			`{"token":"synthetic-token"}`, &authv1.ConfirmEmailVerificationRequest{Token: "synthetic-token"}, &authv1.ConfirmEmailVerificationResponse{Message: "private backend state", UserId: "owner"}, http.StatusOK},
		{"RequestPasswordReset", func(h *handler.AuthHandler) http.HandlerFunc { return h.RequestPasswordReset },
			`{"email":"unknown@example.invalid"}`, &authv1.RequestPasswordResetRequest{Email: "unknown@example.invalid"}, &authv1.RequestPasswordResetResponse{Message: "private backend state"}, http.StatusAccepted},
		{"ConfirmPasswordReset", func(h *handler.AuthHandler) http.HandlerFunc { return h.ConfirmPasswordReset },
			`{"token":"synthetic-token","password":"NewPassword12"}`, &authv1.ConfirmPasswordResetRequest{Token: "synthetic-token", Password: "NewPassword12"}, &authv1.ConfirmPasswordResetResponse{Message: "private backend state", UserId: "owner"}, http.StatusOK},
		{"RequestEmailChange", func(h *handler.AuthHandler) http.HandlerFunc { return h.RequestEmailChange },
			`{"password":"FreshPassword12","new_email":"new@example.invalid"}`, &authv1.RequestEmailChangeRequest{AccessToken: "own-access", Password: "FreshPassword12", NewEmail: "new@example.invalid"}, &authv1.RequestEmailChangeResponse{Message: "private backend state"}, http.StatusAccepted},
		{"ConfirmEmailChange", func(h *handler.AuthHandler) http.HandlerFunc { return h.ConfirmEmailChange },
			`{"token":"synthetic-token"}`, &authv1.ConfirmEmailChangeRequest{Token: "synthetic-token"}, &authv1.ConfirmEmailChangeResponse{Message: "private backend state", UserId: "owner"}, http.StatusOK},
	}
}

func emailIdentityRequest(body string) *http.Request {
	ctx := middleware.WithIdentity(context.Background(), middleware.Identity{UserID: "owner", Token: "own-access"})
	return newJSONRequest("/", body).WithContext(ctx)
}

func TestEmailRESTEndpointsForwardOnlyTheirDocumentedFields(t *testing.T) {
	for _, tc := range emailEndpoints() {
		t.Run(tc.name, func(t *testing.T) {
			h, client, _ := newAuthHandler(t)
			client.On(tc.name, mock.Anything, tc.request).Return(tc.reply, nil).Once()
			w := httptest.NewRecorder()
			tc.handle(h)(w, emailIdentityRequest(tc.body))
			require.Equal(t, tc.status, w.Code)
			require.NotContains(t, w.Body.String(), "private backend")
			require.NotContains(t, w.Body.String(), "synthetic-token")
			require.NotContains(t, w.Body.String(), "Password12")
			require.NotContains(t, w.Body.String(), "access_token")
			require.NotContains(t, w.Body.String(), "refresh_token")
			if tc.status == http.StatusAccepted {
				require.JSONEq(t, `{"message":"If the request is eligible, an email will be sent."}`, w.Body.String())
			} else {
				require.Contains(t, w.Body.String(), `"user_id":"owner"`)
			}
		})
	}
}

func TestEmailRESTRejectsUnknownJSONAndMalformedInputBeforeBackend(t *testing.T) {
	for _, tc := range emailEndpoints() {
		for _, body := range []string{"!", tc.body + ` {}`, strings.TrimSuffix(tc.body, "}") + `,"user_id":"other"}`} {
			h, _, _ := newAuthHandler(t)
			w := httptest.NewRecorder()
			tc.handle(h)(w, emailIdentityRequest(body))
			require.Equal(t, http.StatusBadRequest, w.Code, tc.name)
		}
	}
}

func TestEmailRESTEmptyProofComesFromServiceAsInvalidEmailAction(t *testing.T) {
	for _, tc := range emailEndpoints() {
		if !strings.HasPrefix(tc.name, "Confirm") {
			continue
		}
		h, client, _ := newAuthHandler(t)
		client.On(tc.name, mock.Anything, mock.Anything).Return(nil, emailStatus(t, codes.InvalidArgument, "INVALID_EMAIL_ACTION", "auth.v1")).Once()
		w := httptest.NewRecorder()
		tc.handle(h)(w, emailIdentityRequest(`{}`))
		require.Equal(t, http.StatusBadRequest, w.Code)
		require.JSONEq(t, `{"error":{"code":"invalid_email_action","message":"invalid email action"}}`, w.Body.String())
	}
}

func TestEmailRESTRejectsMissingAuthenticationForPrivateOperations(t *testing.T) {
	for _, get := range []bool{false, true} {
		h, _, _ := newAuthHandler(t)
		w := httptest.NewRecorder()
		if get {
			h.Account(w, httptest.NewRequest(http.MethodGet, "/", nil))
		} else {
			h.RequestEmailChange(w, newJSONRequest("/", `{}`))
		}
		require.Equal(t, http.StatusUnauthorized, w.Code)
	}
}

func TestAccountRESTForwardsOwnTokenAndDoesNotExposeAnotherIdentity(t *testing.T) {
	for _, foreign := range []bool{false, true} {
		h, client, _ := newAuthHandler(t)
		owner := "owner"
		if foreign {
			owner = "other"
		}
		deadline := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
		client.EXPECT().GetAccount(mock.Anything, &authv1.GetAccountRequest{AccessToken: "own-access"}).Return(&authv1.GetAccountResponse{
			UserId: owner, Email: "alice@example.invalid", Username: "alice", VerificationRequired: true, VerificationDeadline: timestamppb.New(deadline),
		}, nil)
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/api/v1/auth/account?user_id=other", nil).WithContext(emailIdentityRequest("").Context())
		h.Account(w, r)
		if foreign {
			require.Equal(t, http.StatusInternalServerError, w.Code)
			require.NotContains(t, w.Body.String(), "alice")
		} else {
			require.Equal(t, http.StatusOK, w.Code)
			require.JSONEq(t, `{"user_id":"owner","email":"alice@example.invalid","username":"alice","email_verified":false,"verification_required":true,"verification_deadline":"2026-11-02T12:00:00Z"}`, w.Body.String())
		}
	}
}

func TestRegistrationRESTOptionalVerificationDoesNotChangeLegacyShape(t *testing.T) {
	h, client, _ := newAuthHandler(t)
	client.EXPECT().Register(mock.Anything, mock.Anything).Return(&authv1.RegisterResponse{UserId: "owner", VerificationRequired: proto.Bool(true)}, nil)
	w := httptest.NewRecorder()
	h.Register(w, newJSONRequest("/", `{}`))
	require.Equal(t, http.StatusCreated, w.Code)
	require.JSONEq(t, `{"user_id":"owner","email":"","username":"","verification_required":true}`, w.Body.String())
}

func TestEmailVerificationRequiredMapsOnlyTheAuthReason(t *testing.T) {
	for _, tc := range []struct {
		reason, domain string
		want           int
		code           string
	}{
		{"EMAIL_VERIFICATION_REQUIRED", "auth.v1", http.StatusForbidden, "email_verification_required"},
		{"OTHER_PRECONDITION", "auth.v1", http.StatusConflict, "failed_precondition"},
		{"EMAIL_VERIFICATION_REQUIRED", "other.v1", http.StatusConflict, "failed_precondition"},
	} {
		t.Run(tc.domain+tc.reason, func(t *testing.T) {
			h, client, _ := newAuthHandler(t)
			client.EXPECT().Login(mock.Anything, mock.Anything).Return(nil, emailStatus(t, codes.FailedPrecondition, tc.reason, tc.domain))
			w := httptest.NewRecorder()
			h.Login(w, newJSONRequest("/api/v1/auth/login", `{"email":"alice@example.invalid","password":"Password12"}`))
			require.Equal(t, tc.want, w.Code)
			require.Contains(t, w.Body.String(), `"code":"`+tc.code+`"`)
		})
	}
}
