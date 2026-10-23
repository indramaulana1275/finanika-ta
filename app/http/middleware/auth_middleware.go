package middleware

import (
	"strings"

	"github.com/golang-jwt/jwt/v5"
	"github.com/goravel/framework/contracts/http"

	"edukasi-finansial-goedukasi-finansial-go/app/facades"
)

const (
	contextUserID   = "user_id"
	contextUserRole = "user_role"
)

type AuthMiddleware struct{}

func NewAuthMiddleware() http.Middleware {
	return &AuthMiddleware{}
}

func (m *AuthMiddleware) Signature() string {
	return "auth"
}

func (m *AuthMiddleware) Handle(ctx http.Context) {
	tokenString, ok := bearerToken(ctx.Request().Header("Authorization"))
	if !ok {
		unauthorized(ctx, "Authorization header must be a Bearer token")
		return
	}

	token, err := jwt.Parse(tokenString, func(token *jwt.Token) (any, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, jwt.ErrSignatureInvalid
		}

		return []byte(facades.Config().GetString("jwt.secret")), nil
	}, jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}))
	if err != nil || !token.Valid {
		unauthorized(ctx, "Invalid or expired token")
		return
	}

	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		unauthorized(ctx, "Invalid token claims")
		return
	}

	ctx.WithValue(contextUserID, claims["sub"])
	ctx.WithValue(contextUserRole, claims["role"])
	ctx.Request().Next()
}

func bearerToken(header string) (string, bool) {
	token, found := strings.CutPrefix(header, "Bearer ")
	if !found {
		return "", false
	}

	token = strings.TrimSpace(token)
	if token == "" {
		return "", false
	}

	return token, true
}

func unauthorized(ctx http.Context, message string) {
	ctx.Response().Json(http.StatusUnauthorized, http.Json{"error": message}).Abort()
}
