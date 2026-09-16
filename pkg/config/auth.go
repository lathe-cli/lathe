package config

import (
	"fmt"
	"strings"
)

const (
	AuthLoginOAuthDevice = "oauth_device"
	AuthLoginHostname    = "${hostname}"
	AuthLoginProvider    = "${provider}"
	AuthLoginDeviceLabel = "${device_label}"
	AuthLoginDeviceCode  = "${device_code}"
)

type AuthInfo struct {
	DefaultType  string        `yaml:"default_type,omitempty"`
	APIKeyHeader string        `yaml:"api_key_header,omitempty"`
	Validate     *AuthValidate `yaml:"validate,omitempty"`
	Login        *AuthLogin    `yaml:"login,omitempty"`
}

type AuthLogin struct {
	Type         string                `yaml:"type"`
	StartPath    string                `yaml:"start_path"`
	TokenPath    string                `yaml:"token_path"`
	RefreshPath  string                `yaml:"refresh_path,omitempty"`
	StartRequest map[string]string     `yaml:"start_request,omitempty"`
	PollRequest  map[string]string     `yaml:"poll_request,omitempty"`
	PollResponse AuthLoginPollResponse `yaml:"poll_response,omitempty"`
}

type AuthLoginPollResponse struct {
	Status       string            `yaml:"status,omitempty"`
	Error        string            `yaml:"error,omitempty"`
	AccessToken  string            `yaml:"access_token,omitempty"`
	RefreshToken string            `yaml:"refresh_token,omitempty"`
	ExpiresIn    string            `yaml:"expires_in,omitempty"`
	UserEmail    string            `yaml:"user_email,omitempty"`
	UserName     string            `yaml:"user_name,omitempty"`
	Contexts     map[string]string `yaml:"contexts,omitempty"`
}

type AuthValidate struct {
	Method  string              `yaml:"method"`
	Path    string              `yaml:"path"`
	Display AuthValidateDisplay `yaml:"display"`
	Assert  *AuthValidateAssert `yaml:"assert,omitempty"`
}

type AuthValidateDisplay struct {
	UsernameField string `yaml:"username_field"`
	FallbackField string `yaml:"fallback_field"`
}

type AuthValidateAssert struct {
	Field    string `yaml:"field,omitempty"`
	NonEmpty bool   `yaml:"non_empty,omitempty"`
}

func validateAuthLoginRequest(name string, request map[string]string, allowed map[string]bool) error {
	for field, value := range request {
		if strings.TrimSpace(field) == "" {
			return fmt.Errorf("auth.login.%s field names must not be empty", name)
		}
		if strings.Contains(value, "${") && !allowed[value] {
			return fmt.Errorf("auth.login.%s field %q has unsupported placeholder %q", name, field, value)
		}
	}
	return nil
}

func normalizeAuth(auth *AuthInfo, contexts map[string]ContextInfo) error {
	auth.DefaultType = strings.ToLower(strings.TrimSpace(auth.DefaultType))
	auth.APIKeyHeader = strings.TrimSpace(auth.APIKeyHeader)
	switch auth.DefaultType {
	case "", "bearer", "apikey", "basic", "oauth":
	default:
		return fmt.Errorf("auth.default_type must be one of bearer, apikey, basic, or oauth")
	}
	if auth.Validate != nil && auth.Validate.Assert != nil {
		auth.Validate.Assert.Field = strings.TrimSpace(auth.Validate.Assert.Field)
		if auth.Validate.Assert.Field == "" && !auth.Validate.Assert.NonEmpty {
			return fmt.Errorf("auth.validate.assert requires field or non_empty")
		}
	}
	login := auth.Login
	if login == nil {
		if auth.DefaultType == "oauth" {
			return fmt.Errorf("auth.default_type oauth requires an auth.login block")
		}
		return nil
	}
	login.Type = strings.ToLower(strings.TrimSpace(login.Type))
	login.StartPath = strings.TrimSpace(login.StartPath)
	login.TokenPath = strings.TrimSpace(login.TokenPath)
	login.RefreshPath = strings.TrimSpace(login.RefreshPath)
	if login.Type != AuthLoginOAuthDevice {
		return fmt.Errorf("auth.login.type must be %q", AuthLoginOAuthDevice)
	}
	if login.StartPath == "" || login.TokenPath == "" {
		return fmt.Errorf("auth.login.start_path and auth.login.token_path are required")
	}
	if !strings.HasPrefix(login.StartPath, "/") || !strings.HasPrefix(login.TokenPath, "/") {
		return fmt.Errorf("auth.login.start_path and auth.login.token_path must start with /")
	}
	if login.RefreshPath != "" && !strings.HasPrefix(login.RefreshPath, "/") {
		return fmt.Errorf("auth.login.refresh_path must start with /")
	}
	if err := validateAuthLoginRequest("start_request", login.StartRequest, map[string]bool{
		AuthLoginHostname: true, AuthLoginProvider: true, AuthLoginDeviceLabel: true,
	}); err != nil {
		return err
	}
	if err := validateAuthLoginRequest("poll_request", login.PollRequest, map[string]bool{
		AuthLoginHostname: true, AuthLoginProvider: true, AuthLoginDeviceLabel: true, AuthLoginDeviceCode: true,
	}); err != nil {
		return err
	}
	fields := &login.PollResponse
	fields.Status = strings.TrimSpace(fields.Status)
	fields.Error = strings.TrimSpace(fields.Error)
	fields.AccessToken = strings.TrimSpace(fields.AccessToken)
	fields.RefreshToken = strings.TrimSpace(fields.RefreshToken)
	fields.ExpiresIn = strings.TrimSpace(fields.ExpiresIn)
	fields.UserEmail = strings.TrimSpace(fields.UserEmail)
	fields.UserName = strings.TrimSpace(fields.UserName)
	for name, path := range fields.Contexts {
		if _, ok := contexts[name]; !ok {
			return fmt.Errorf("auth.login.poll_response.contexts references unknown context %q", name)
		}
		path = strings.TrimSpace(path)
		if path == "" {
			return fmt.Errorf("auth.login.poll_response.contexts.%s must not be empty", name)
		}
		fields.Contexts[name] = path
	}
	return nil
}
