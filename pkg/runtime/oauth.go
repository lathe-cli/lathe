package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/lathe-cli/lathe/pkg/config"
)

const oauthRefreshSkew = 60

type oauthTokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int64  `json:"expires_in"`
}

func refreshAuthFunc(hostname string, insecure bool, rejectedToken string) func(context.Context) (Authenticator, error) {
	return func(ctx context.Context) (Authenticator, error) {
		hosts, err := config.LoadHosts()
		if err != nil {
			return nil, err
		}
		entry, ok := hosts.Get(hostname)
		if !ok {
			return nil, notAuthenticatedToHost(hostname)
		}
		if entry.OAuthToken != "" && entry.OAuthToken != rejectedToken {
			return NewAuthFromHost(entry)
		}
		entry, err = refreshHostAuth(ctx, hostname, entry, insecure)
		if err != nil {
			return nil, err
		}
		return NewAuthFromHost(entry)
	}
}

func canRefreshHostAuth(entry config.HostEntry) bool {
	login := config.Active().Auth.Login
	return login != nil && login.RefreshPath != "" && entry.OAuthRefreshToken != ""
}

func refreshHostAuthIfNeeded(ctx context.Context, hostname string, entry config.HostEntry, insecure bool) (config.HostEntry, error) {
	if !canRefreshHostAuth(entry) {
		return entry, nil
	}
	if entry.OAuthExpiresAt == 0 || time.Now().Unix()+oauthRefreshSkew < entry.OAuthExpiresAt {
		return entry, nil
	}
	return refreshHostAuth(ctx, hostname, entry, insecure)
}

func refreshHostAuth(ctx context.Context, hostname string, entry config.HostEntry, insecure bool) (config.HostEntry, error) {
	login := config.Active().Auth.Login
	if login == nil || login.RefreshPath == "" || entry.OAuthRefreshToken == "" {
		return entry, fmt.Errorf("refresh token unavailable; run `%s auth login --auth-type oauth --hostname %s`", config.Active().CLI.Name, hostname)
	}
	data, err := DoRaw(ctx, hostname, "POST", login.RefreshPath, map[string]string{
		"refresh_token": entry.OAuthRefreshToken,
	}, ClientOptions{Insecure: insecure, Timeout: 10 * time.Second})
	if err != nil {
		if current, ok := refreshedByAnotherProcess(hostname, entry); ok {
			return current, nil
		}
		return entry, err
	}
	var token oauthTokenResponse
	if err := json.Unmarshal(data, &token); err != nil {
		return entry, fmt.Errorf("decode refresh token response: %w", err)
	}
	if token.AccessToken == "" {
		return entry, fmt.Errorf("refresh token response missing access_token")
	}
	updated := entry
	err = config.MutateHosts(ctx, func(hosts *config.Hosts) error {
		current, ok := hosts.Get(hostname)
		if !ok {
			return notAuthenticatedToHost(hostname)
		}
		if current.OAuthToken != entry.OAuthToken || current.OAuthRefreshToken != entry.OAuthRefreshToken {
			updated = current
			return nil
		}
		current.AuthType = "bearer"
		current.OAuthToken = token.AccessToken
		if token.RefreshToken != "" {
			current.OAuthRefreshToken = token.RefreshToken
		}
		if token.ExpiresIn > 0 {
			current.OAuthExpiresAt = time.Now().Add(time.Duration(token.ExpiresIn) * time.Second).Unix()
		} else {
			current.OAuthExpiresAt = 0
		}
		updated = current
		hosts.Set(hostname, current)
		return nil
	})
	return updated, err
}

func refreshedByAnotherProcess(hostname string, old config.HostEntry) (config.HostEntry, bool) {
	hosts, err := config.LoadHosts()
	if err != nil {
		return config.HostEntry{}, false
	}
	current, ok := hosts.Get(hostname)
	if !ok || current.AuthType != "bearer" || current.OAuthToken == "" {
		return config.HostEntry{}, false
	}
	if current.OAuthToken == old.OAuthToken && current.OAuthRefreshToken == old.OAuthRefreshToken {
		return config.HostEntry{}, false
	}
	return current, true
}
