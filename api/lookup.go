package api

import (
	"errors"
	"net/http"
	"strings"

	raven "github.com/getsentry/raven-go"
	"github.com/labstack/echo"
	tinystat "github.com/sdwolfe32/tinystat/client"
	"github.com/sdwolfe32/trumail/verifier"
)

var (
	// ErrValidationFailure indicates that there was an error while validating an email
	ErrValidationFailure = echo.NewHTTPError(http.StatusInternalServerError, "Error validating email")
	// ErrUnsupportedFormat indicates that the requestor has defined an unsupported response format
	ErrUnsupportedFormat = echo.NewHTTPError(http.StatusBadRequest, "Unsupported format")
	// ErrInvalidCallback indicates that the request is missing the callback queryparam
	ErrInvalidCallback = echo.NewHTTPError(http.StatusBadRequest, "Invalid callback query param provided")
)

// Lookup performs a single email validation and returns a fully
// populated lookup or an error
func (t *TrumailAPI) Lookup(c echo.Context) error {
	l := t.log.WithField("handler", "Lookup")
	l.Debug("New Lookup request received")

	// Decode the request
	l.Debug("Decoding the request")
	email := c.Param("email")
	l = l.WithField("email", email)

	// Performs the full email validation
	l.Debug("Performing new validation lookup")
	lookups := t.verify.Verify(email)
	if len(lookups) == 0 {
		l.WithError(ErrValidationFailure).Error("Failed to validate email")
		return ErrValidationFailure
	}
	lookup := lookups[0]
	l = l.WithField("lookup", lookup)

	// If blocked with spamhaus or banned trigger a Heroku dyno restart
	if strings.Contains(strings.ToLower(lookup.ErrorDetails), "spamhaus") ||
		strings.Contains(strings.ToLower(lookup.ErrorDetails), "banned") {
		go restartDyno(t.herokuAppID, t.herokuToken)
	}

	// Return an error response code if there's an error
	if lookup.Error != "" || lookup.ErrorDetails != "" {
		l.Error("Error performing lookup")
		return t.encodeLookup(c, http.StatusInternalServerError, lookup)
	}

	// Returns the email validation lookup to the requestor
	l.Debug("Returning Email Lookup")
	return t.encodeLookup(c, http.StatusOK, lookup)
}

// encodeLookup encodes the passed response using the "format" and
// "callback" parameters on the passed echo.Context
func (t *TrumailAPI) encodeLookup(c echo.Context, code int, lookup *verifier.Lookup) error {
	// Send metrics of response
	if code == http.StatusOK {
		if lookup.Deliverable {
			tinystat.CreateAction("deliverable")
		} else {
			tinystat.CreateAction("undeliverable")
		}
	} else {
		tinystat.CreateAction("error")
	}

	// Report the error to Sentry
	if lookup.ErrorDetails != "" {
		raven.CaptureError(errors.New(lookup.ErrorDetails), nil)
	}

	// Encode the in requested format
	switch c.Param("format") {
	case "json":
		return c.JSON(code, lookup)
	case "jsonp":
		callback := c.QueryParam("callback")
		if callback == "" {
			return ErrInvalidCallback
		}
		return c.JSONP(code, callback, lookup)
	case "xml":
		return c.XML(code, lookup)
	default:
		return ErrUnsupportedFormat
	}
}