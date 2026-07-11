package middleware

import (
	"context"
	"net/http"
	"strings"

	"bknd-3/internal/services"

	"github.com/golang-jwt/jwt/v5"
	"go.uber.org/zap"
)

type AuthMiddleware struct {
	publicKey   interface{}
	authService *services.AuthService
	logr        *zap.Logger
}

type contextKey string

const (
	ContextUserIDKey  contextKey = "userID"
	ContextAuthMethod contextKey = "authMethod"
)

// NewAuthMiddleware creates a reusable JWT auth middleware instance
func NewAuthMiddleware(publicKey interface{}, authService *services.AuthService, logr *zap.Logger) *AuthMiddleware {
	return &AuthMiddleware{
		publicKey:   publicKey,
		authService: authService,
		logr:        logr,
	}
}

// JWTAuth validates the token and attaches user info to request context
func (m *AuthMiddleware) JWTAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader := r.Header.Get("Authorization")
		if authHeader == "" {
			http.Error(w, "missing authorization header", http.StatusUnauthorized)
			return
		}

		tokenString := strings.TrimPrefix(authHeader, "Bearer ")
		if tokenString == authHeader {
			http.Error(w, "invalid token format", http.StatusUnauthorized)
			return
		}

		// Parse token (RS256)
		token, err := jwt.Parse(tokenString, func(token *jwt.Token) (interface{}, error) {
			if _, ok := token.Method.(*jwt.SigningMethodRSA); !ok {
				return nil, jwt.ErrSignatureInvalid
			}
			return m.publicKey, nil
		})

		if err != nil {
			m.logr.Warn("token parse error", zap.Error(err))
			http.Error(w, "invalid or expired token", http.StatusUnauthorized)
			return
		}

		if !token.Valid {
			http.Error(w, "invalid or expired token", http.StatusUnauthorized)
			return
		}

		claims, ok := token.Claims.(jwt.MapClaims)
		if !ok {
			http.Error(w, "invalid token claims", http.StatusUnauthorized)
			return
		}

		userID, _ := claims["sub"].(string)
		authMethod, _ := claims["auth_method"].(string)
		tokenVersionFloat, _ := claims["ver"].(float64)
		tokenVersion := int(tokenVersionFloat)

		// Validate token version from DB
		valid, err := m.authService.CheckTokenVersion(r.Context(), userID, tokenVersion)
		if err != nil {
			m.logr.Error("failed checking token version", zap.Error(err), zap.String("user_id", userID))
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		if !valid {
			m.logr.Warn("token version invalid", zap.String("user_id", userID))
			http.Error(w, "token revoked or invalid", http.StatusUnauthorized)
			return
		}

		// Attach user info to request context
		ctx := context.WithValue(r.Context(), ContextUserIDKey, userID)
		ctx = context.WithValue(ctx, ContextAuthMethod, authMethod)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
