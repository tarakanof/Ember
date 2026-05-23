package main

import "strings"

// producer.env keys the settings window edits.
const (
	keySource     = "STATUS_SOURCE"
	keyServerURL  = "STATUS_SERVER_URL"
	keyToken      = "STATUS_TOKEN"
	keyColor      = "STATUS_SOURCE_COLOR"
	keyContextPct = "STATUS_CONTEXT_PCT_ENABLED"
	keyRatePct        = "STATUS_RATE_PCT_ENABLED"
	keyActivityDetail = "STATUS_ACTIVITY_DETAIL_ENABLED"
	keyActivityTrail  = "STATUS_ACTIVITY_TRAIL_ENABLED"
	keyContextNumber  = "STATUS_CONTEXT_NUMBER_ENABLED"
	keyRateBottomBar  = "STATUS_RATE_BOTTOM_BAR"
	keyRateReset      = "STATUS_RATE_RESET"
)

// settingsForm is the editable view of producer.env shown in the window.
// Token is write-only: "" means "keep the current value".
type settingsForm struct {
	Source        string
	ServerURL     string
	SourceColor   string
	ContextPct     bool
	RatePct        bool
	ActivityDetail bool
	ActivityTrail  bool
	ContextNumber  bool
	RateBottomBar  bool
	RateReset      bool
	Token          string // "" = keep current
}

// formFromEnv builds the form from rec. The bool reports whether a token is
// currently set (drives the (set)/(unset) placeholder); Token loads blank so
// the secret never round-trips into the UI.
func formFromEnv(rec *envRec) (settingsForm, bool) {
	return settingsForm{
		Source:        rec.get(keySource),
		ServerURL:     rec.get(keyServerURL),
		SourceColor:   rec.get(keyColor),
		ContextPct:     isEnvTrue(rec.get(keyContextPct)),
		RatePct:        isEnvTrue(rec.get(keyRatePct)),
		ActivityDetail: isEnvTrue(rec.get(keyActivityDetail)),
		ActivityTrail:  isEnvTrue(rec.get(keyActivityTrail)),
		ContextNumber:  isEnvOn(rec.get(keyContextNumber)),
		RateBottomBar:  isEnvOn(rec.get(keyRateBottomBar)),
		RateReset:      isEnvOn(rec.get(keyRateReset)),
		Token:          "",
	}, rec.get(keyToken) != ""
}

// validateForm validates the four text fields via validateSetting, plus the
// token only when non-blank. Blank token + checkboxes are not errors. Returns
// producer.env-key -> message; empty map means valid.
func validateForm(f settingsForm) map[string]string {
	errs := map[string]string{}
	for _, c := range []struct{ key, val string }{
		{keySource, f.Source},
		{keyServerURL, f.ServerURL},
		{keyColor, f.SourceColor},
	} {
		if _, err := validateSetting(c.key, c.val); err != nil {
			errs[c.key] = err.Error()
		}
	}
	if strings.TrimSpace(f.Token) != "" {
		if _, err := validateSetting(keyToken, f.Token); err != nil {
			errs[keyToken] = err.Error()
		}
	}
	return errs
}

// applyForm writes a (already-validated) form into rec; the caller persists via
// writeEnvAtomic. Token blank keeps the existing value; non-blank replaces.
// Text values are normalized via validateSetting; checkboxes become true/false.
func applyForm(rec *envRec, f settingsForm) {
	norm := func(key, val string) string { v, _ := validateSetting(key, val); return v }
	rec.set(keySource, norm(keySource, f.Source))
	rec.set(keyServerURL, norm(keyServerURL, f.ServerURL))
	rec.set(keyColor, norm(keyColor, f.SourceColor))
	rec.set(keyContextPct, boolStr(f.ContextPct))
	rec.set(keyRatePct, boolStr(f.RatePct))
	rec.set(keyActivityDetail, boolStr(f.ActivityDetail))
	rec.set(keyActivityTrail, boolStr(f.ActivityTrail))
	rec.set(keyContextNumber, boolStr(f.ContextNumber))
	rec.set(keyRateBottomBar, boolStr(f.RateBottomBar))
	rec.set(keyRateReset, boolStr(f.RateReset))
	if strings.TrimSpace(f.Token) != "" {
		rec.set(keyToken, norm(keyToken, f.Token))
	}
}

func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}
