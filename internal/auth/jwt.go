package auth

import (
	"crypto/rsa"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/ioutil"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

type TokenKind string

const (
	AccessToken  TokenKind = "access"
	RefreshToken TokenKind = "refresh"
)

type JWTManager struct {
	privateKey *rsa.PrivateKey
	publicKey  *rsa.PublicKey
	issuer     string
}

type TokenPair struct {
	AccessToken  string
	RefreshToken string
	AccessExp    time.Time
	RefreshExp   time.Time
	JTI          string
}

func NewJWTManager(privatePath, publicPath, issuer string) (*JWTManager, error) {
	privPem, err := ioutil.ReadFile(privatePath)
	if err != nil {
		return nil, fmt.Errorf("read private key: %w", err)
	}
	privKey, err := jwt.ParseRSAPrivateKeyFromPEM(privPem)
	if err != nil {
		return nil, fmt.Errorf("parse private key: %w", err)
	}

	pubPem, err := ioutil.ReadFile(publicPath)
	if err != nil {
		return nil, fmt.Errorf("read public key: %w", err)
	}
	pubKey, err := jwt.ParseRSAPublicKeyFromPEM(pubPem)
	if err != nil {
		return nil, fmt.Errorf("parse public key: %w", err)
	}

	return &JWTManager{
		privateKey: privKey,
		publicKey:  pubKey,
		issuer:     issuer,
	}, nil
}

// createJWT makes a signed JWT for given claims
func (m *JWTManager) createJWT(userID string, kind TokenKind, ttl time.Duration, tokenVersion int, jti string, authMethod string, roles []string) (string, time.Time, error) {
	now := time.Now().UTC()
	exp := now.Add(ttl)

	claims := jwt.MapClaims{
		"iss":         m.issuer,
		"sub":         userID,
		"iat":         now.Unix(),
		"exp":         exp.Unix(),
		"jti":         jti,
		"typ":         string(kind),
		"ver":         tokenVersion,
		"auth_method": authMethod,
	}
	if len(roles) > 0 {
		claims["roles"] = roles
	}

	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	tokenStr, err := token.SignedString(m.privateKey)
	if err != nil {
		return "", time.Time{}, err
	}
	return tokenStr, exp, nil
}

// GenerateTokenPair – create access + refresh tokens
func (m *JWTManager) GenerateTokenPair(userID string, accessTTL, refreshTTL time.Duration, tokenVersion int, authMethod string, roles []string) (*TokenPair, error) {
	jti := uuid.New().String()
	accessToken, accessExp, err := m.createJWT(userID, AccessToken, accessTTL, tokenVersion, jti, authMethod, roles)
	if err != nil {
		return nil, err
	}

	// refresh token we will make as a JWT too with its own jti (or could be opaque)
	refreshJTI := uuid.New().String()
	refreshToken, refreshExp, err := m.createJWT(userID, RefreshToken, refreshTTL, tokenVersion, refreshJTI, authMethod, roles)
	if err != nil {
		return nil, err
	}

	return &TokenPair{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		AccessExp:    accessExp,
		RefreshExp:   refreshExp,
		JTI:          jti,
	}, nil
}

// VerifyAccessToken verifies and returns claims (use for middleware). It checks RS256 signature and expiry.
func (m *JWTManager) VerifyToken(tokenStr string) (jwt.MapClaims, error) {
	token, err := jwt.Parse(tokenStr, func(token *jwt.Token) (interface{}, error) {
		if token.Method != jwt.SigningMethodRS256 {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return m.publicKey, nil
	}, jwt.WithLeeway(5*time.Second))
	if err != nil {
		return nil, err
	}
	if claims, ok := token.Claims.(jwt.MapClaims); ok && token.Valid {
		return claims, nil
	}
	return nil, errors.New("invalid token")
}

// HashToken produces SHA256 hex of the token for storage
func HashToken(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}
