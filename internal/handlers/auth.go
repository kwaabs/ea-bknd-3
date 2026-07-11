package handlers

import (
	"bknd-3/internal/config"
	"bknd-3/internal/logger"
	"bknd-3/internal/services"
	"encoding/json"
	"go.uber.org/zap"
	"net/http"
	"time"
)

type AuthHandler struct {
	authSvc *services.AuthService
	logr    *logger.Logger
	cfg     *config.Config
}

func NewAuthHandler(svc *services.AuthService, logr *logger.Logger, cfg *config.Config) *AuthHandler {
	return &AuthHandler{authSvc: svc, logr: logr, cfg: cfg}
}

type loginReq struct {
	Email      string `json:"email"`
	Password   string `json:"password"`
	DeviceInfo string `json:"device_info"`
}

// 👇 add here
type azureReq struct {
    IDToken    string `json:"id_token"`
    AccessToken string `json:"access_token"` // 👈 add this
    DeviceInfo string `json:"device_info"`
}

type ldapReq struct {
	Username   string `json:"username"`
	Password   string `json:"password"`
	DeviceInfo string `json:"device_info"`
}

type userInfo struct {
	ID       string   `json:"id"`
	Email    string   `json:"email"`
	Name     string   `json:"name"`
	Provider string   `json:"provider"`
	Roles    []string `json:"roles"`
}

type tokenResp struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	ExpiresAt    time.Time `json:"access_expires_at"`
	User         *userInfo `json:"user"` // Added user info
}

// POST /auth/login
func (h *AuthHandler) LoginLocal(w http.ResponseWriter, r *http.Request) {
	var req loginReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid payload", http.StatusBadRequest)
		return
	}
	pair, user, err := h.authSvc.LoginLocal(r.Context(), req.Email, req.Password, req.DeviceInfo)
	if err != nil {
		h.logr.Warn("local login failed", zap.Error(err), zap.String("email", req.Email))
		http.Error(w, "invalid credentials", http.StatusUnauthorized)
		return
	}

	// set refresh cookie and return JSON
	h.setRefreshCookie(w, pair.RefreshToken, pair.RefreshExp)
	resp := tokenResp{
		AccessToken:  pair.AccessToken,
		RefreshToken: pair.RefreshToken,
		ExpiresAt:    pair.AccessExp,
		User: &userInfo{
			ID:       user.ID,
			Email:    user.Email,
			Name:     user.Name,
			Provider: user.Provider,
			Roles:    user.Roles,
		},
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// POST /auth/ldap
func (h *AuthHandler) LoginLDAP(w http.ResponseWriter, r *http.Request) {
	var req ldapReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid payload", http.StatusBadRequest)
		return
	}
	pair, user, err := h.authSvc.LoginLDAP(r.Context(), req.Username, req.Password, req.DeviceInfo)
	if err != nil {
		h.logr.Warn("ldap login failed", zap.Error(err), zap.String("username", req.Username))
		http.Error(w, "invalid credentials", http.StatusUnauthorized)
		return
	}

	h.setRefreshCookie(w, pair.RefreshToken, pair.RefreshExp)
	resp := tokenResp{
		AccessToken:  pair.AccessToken,
		RefreshToken: pair.RefreshToken,
		ExpiresAt:    pair.AccessExp,
		User: &userInfo{
			ID:       user.ID,
			Email:    user.Email,
			Name:     user.Name,
			Provider: user.Provider,
			Roles:    user.Roles,
		},
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// POST /auth/refresh  (reads refresh token from cookie OR body)
type refreshReq struct {
	RefreshToken string `json:"refresh_token,omitempty"`
	DeviceInfo   string `json:"device_info,omitempty"`
}

func (h *AuthHandler) Refresh(w http.ResponseWriter, r *http.Request) {
	var req refreshReq
	_ = json.NewDecoder(r.Body).Decode(&req)

	// prefer cookie if present
	cookie, err := r.Cookie("refresh_token")
	if err == nil && cookie.Value != "" {
		req.RefreshToken = cookie.Value
	}

	if req.RefreshToken == "" {
		http.Error(w, "refresh token required", http.StatusBadRequest)
		return
	}

	pair, err := h.authSvc.Refresh(r.Context(), req.RefreshToken, req.DeviceInfo)
	if err != nil {
		h.logr.Warn("refresh failed", zap.Error(err))
		http.Error(w, "invalid refresh token", http.StatusUnauthorized)
		return
	}

	h.setRefreshCookie(w, pair.RefreshToken, pair.RefreshExp)
	resp := tokenResp{
		AccessToken:  pair.AccessToken,
		RefreshToken: pair.RefreshToken,
		ExpiresAt:    pair.AccessExp,
		User:         nil, // No user info on refresh (token already has user claims)
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// POST /auth/logout
type logoutReq struct {
	RefreshToken string `json:"refresh_token,omitempty"`
}

func (h *AuthHandler) Logout(w http.ResponseWriter, r *http.Request) {
	var req logoutReq
	_ = json.NewDecoder(r.Body).Decode(&req)

	// prefer cookie
	cookie, err := r.Cookie("refresh_token")
	if err == nil && cookie.Value != "" {
		req.RefreshToken = cookie.Value
	}

	if req.RefreshToken == "" {
		http.Error(w, "refresh token required", http.StatusBadRequest)
		return
	}

	if err := h.authSvc.Logout(r.Context(), req.RefreshToken); err != nil {
		h.logr.Warn("logout failed", zap.Error(err))
		http.Error(w, "failed to logout", http.StatusInternalServerError)
		return
	}

	// clear cookie
	http.SetCookie(w, &http.Cookie{
		Name:     "refresh_token",
		Value:    "",
		Expires:  time.Unix(0, 0),
		HttpOnly: true,
		Secure:   true,
		Path:     "/",
		SameSite: http.SameSiteLaxMode,
	})

	w.WriteHeader(http.StatusNoContent)
}

func (h *AuthHandler) setRefreshCookie(w http.ResponseWriter, token string, expires time.Time) {
	cookie := &http.Cookie{
		Name:     "refresh_token",
		Value:    token,
		Expires:  expires,
		HttpOnly: true,
		Secure:   true,
		Path:     "/",
		SameSite: http.SameSiteLaxMode,
	}
	http.SetCookie(w, cookie)
}

// POST /auth/azure
func (h *AuthHandler) LoginAzureAD(w http.ResponseWriter, r *http.Request) {
    var req azureReq
    if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
        http.Error(w, "invalid payload", http.StatusBadRequest)
        return
    }
    if req.AccessToken == "" { // 👈 changed from IDToken
            http.Error(w, "access_token required", http.StatusBadRequest)
            return
    }

    pair, user, err := h.authSvc.LoginAzureAD(r.Context(), req.IDToken, req.AccessToken, req.DeviceInfo) // 👈 pass both
    if err != nil {
        h.logr.Warn("azure login failed", zap.Error(err))
        http.Error(w, "authentication failed", http.StatusUnauthorized)
        return
    }

    h.setRefreshCookie(w, pair.RefreshToken, pair.RefreshExp)
    resp := tokenResp{
        AccessToken:  pair.AccessToken,
        RefreshToken: pair.RefreshToken,
        ExpiresAt:    pair.AccessExp,
        User: &userInfo{
            ID:       user.ID,
            Email:    user.Email,
            Name:     user.Name,
            Provider: user.Provider,
            Roles:    user.Roles,
        },
    }
    w.Header().Set("Content-Type", "application/json")
    json.NewEncoder(w).Encode(resp)
}
