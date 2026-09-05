package server

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/zhong/droply/internal/model"
	"github.com/zhong/droply/internal/store"
	"net/http"
	"strings"

	"golang.org/x/crypto/bcrypt"
)

// generateToken creates a new API token in the format "dp_" + 32 random hex bytes.
func generateToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "dp_" + hex.EncodeToString(b), nil
}

type registerRequest struct {
	Invite   string `json:"invite"`
	Email    string `json:"email"`
	Password string `json:"password"`
}

type authResponse struct {
	APIToken string `json:"api_token"`
}

// handleRegister creates a new user account and returns an API token.
func (s *Server) handleRegister(w http.ResponseWriter, r *http.Request) {
	if !s.allowAuthentication(w, r) {
		return
	}
	var req registerRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192)).Decode(&req); err != nil {
		jsonError(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if !s.openRegistration && req.Invite == "" {
		jsonError(w, "registration requires an invitation", 403)
		return
	}
	req.Email = strings.TrimSpace(req.Email)
	if req.Email == "" || req.Password == "" {
		jsonError(w, "email and password are required", http.StatusBadRequest)
		return
	}

	hashed, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		jsonError(w, "internal server error", http.StatusInternalServerError)
		return
	}

	token, err := generateToken()
	if err != nil {
		jsonError(w, "internal server error", http.StatusInternalServerError)
		return
	}

	var user *model.User
	if req.Invite != "" {
		user, err = s.store.RegisterInvited(r.Context(), req.Email, string(hashed), token, req.Invite)
	} else {
		user, err = s.store.CreateUser(req.Email, string(hashed), token)
	}
	if errors.Is(err, store.ErrInvitation) {
		jsonError(w, "invitation unavailable or invalid", 403)
		return
	}
	if err != nil {
		// Treat any error from CreateUser with a duplicate email as a conflict.
		if strings.Contains(err.Error(), "UNIQUE") {
			jsonError(w, "email already registered", http.StatusConflict)
			return
		}
		jsonError(w, "internal server error", http.StatusInternalServerError)
		return
	}

	jsonResponse(w, authResponse{APIToken: user.APIToken}, http.StatusCreated)
}

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

// handleLogin authenticates a user and returns their API token.
func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if !s.allowAuthentication(w, r) {
		return
	}
	var req loginRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192)).Decode(&req); err != nil {
		jsonError(w, "invalid request body", http.StatusBadRequest)
		return
	}

	user, err := s.authenticateCredentials(req.Email, req.Password)
	if err != nil {
		jsonError(w, "invalid credentials", http.StatusUnauthorized)
		return
	}

	jsonResponse(w, authResponse{APIToken: user.APIToken}, http.StatusOK)
}

var dummyLoginHash = func() []byte {
	hash, err := bcrypt.GenerateFromPassword([]byte("droply-invalid-account-dummy"), bcrypt.DefaultCost)
	if err != nil {
		panic(err)
	}
	return hash
}()

func (s *Server) authenticateCredentials(email, password string) (*model.User, error) {
	user, err := s.store.GetUserByEmail(strings.TrimSpace(email))
	if err != nil {
		_ = bcrypt.CompareHashAndPassword(dummyLoginHash, []byte(password))
		return nil, errors.New("invalid credentials")
	}
	if err := bcrypt.CompareHashAndPassword([]byte(user.Password), []byte(password)); err != nil {
		return nil, errors.New("invalid credentials")
	}
	return user, nil
}
