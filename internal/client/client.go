// Package client is a thin wrapper around the two Google Analytics APIs the
// CLI speaks: the Data API v1beta (reporting) and the Admin API v1beta
// (accounts, properties, data streams).
package client

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/KLIXPERT-io/ga4-cli/internal/errs"
	analyticsadmin "google.golang.org/api/analyticsadmin/v1beta"
	analyticsdata "google.golang.org/api/analyticsdata/v1beta"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/option"
)

type Client struct {
	Data  *analyticsdata.Service
	Admin *analyticsadmin.Service
}

func New(ctx context.Context, httpClient *http.Client) (*Client, error) {
	data, err := analyticsdata.NewService(ctx, option.WithHTTPClient(httpClient))
	if err != nil {
		return nil, err
	}
	admin, err := analyticsadmin.NewService(ctx, option.WithHTTPClient(httpClient))
	if err != nil {
		return nil, err
	}
	return &Client{Data: data, Admin: admin}, nil
}

// Console links used in hints when an API has not been enabled on the project.
const (
	dataAPILibrary  = "https://console.cloud.google.com/apis/library/analyticsdata.googleapis.com"
	adminAPILibrary = "https://console.cloud.google.com/apis/library/analyticsadmin.googleapis.com"
)

// Translate converts an API error into a structured errs.E.
func Translate(err error) error {
	if err == nil {
		return nil
	}
	var ge *googleapi.Error
	if errors.As(err, &ge) {
		msg := strings.ToLower(ge.Message)
		switch {
		case ge.Code == 401:
			return errs.New(errs.CodeAuthExpired, ge.Message).
				WithHint("Run `ga4 auth login`, or check the service account key with `ga4 auth status`.")

		case ge.Code == 403 && strings.Contains(msg, "has not been used") || ge.Code == 403 && strings.Contains(msg, "service_disabled"):
			return errs.New(errs.CodeAuthDenied, ge.Message).
				WithHint("Enable the API on the credential's Google Cloud project: " + dataAPILibrary + " and " + adminAPILibrary)

		case ge.Code == 403 && (strings.Contains(msg, "quota") || strings.Contains(msg, "exhausted")):
			return errs.New(errs.CodeQuotaExceeded, ge.Message).
				WithHint("Check the remaining token budget with `ga4 quota`. Token buckets refill hourly and daily.").
				WithRetry(3600)

		case ge.Code == 403:
			return errs.New(errs.CodeAuthDenied, ge.Message).
				WithHint("Grant the caller access to the property: GA4 Admin → Property access management → add the OAuth user or the service account's client_email with at least Viewer.")

		case ge.Code == 404:
			return errs.New(errs.CodePropertyNotFound, ge.Message).
				WithHint("Property IDs are numeric (properties/123456789), not the G-XXXXXXX measurement ID. List what you can see with `ga4 properties list`.")

		case ge.Code == 429:
			return errs.New(errs.CodeRateLimited, ge.Message).
				WithHint("The Data API caps concurrent requests per property. Retry shortly.").
				WithRetry(60)

		case ge.Code >= 500:
			return errs.New(errs.CodeAPI5xx, ge.Message).WithRetry(30)

		case ge.Code == 400 && (strings.Contains(msg, "compatib") || strings.Contains(msg, "cannot be used together")):
			return errs.New(errs.CodeIncompatibleFields, ge.Message).
				WithHint("Not every dimension pairs with every metric. Check the combination with `ga4 compat <property> --dimensions ... --metrics ...`.")

		case ge.Code == 400 && (strings.Contains(msg, "did not match") || strings.Contains(msg, "not a valid")):
			return errs.New(errs.CodeInvalidArgs, ge.Message).
				WithHint("Look up exact API field names with `ga4 metadata <property> --search <term>`.")

		case ge.Code >= 400:
			return errs.New(errs.CodeInvalidArgs, ge.Message)
		}
	}
	return TranslateTransport(err.Error())
}

// TranslateTransport classifies errors that never reached the API: token
// acquisition failures and network problems. Credentials that fail to mint a
// token surface here rather than as an HTTP status, so they must still map to
// an auth code — callers branch on the code, not the message.
func TranslateTransport(msg string) error {
	switch {
	case strings.Contains(msg, "oauth2: cannot fetch token"):
		return errs.New(errs.CodeAuthDenied, msg).
			WithHint("Run `ga4 auth status`. For a service account, confirm the key is still active and that any --subject delegation is granted for the analytics.readonly scope.")
	case strings.Contains(msg, "no such host"), strings.Contains(msg, "dial tcp"):
		return errs.New(errs.CodeNetworkUnreachable, msg)
	}
	return errs.New(errs.CodeGeneric, msg)
}
