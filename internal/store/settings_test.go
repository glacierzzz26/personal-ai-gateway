package store

import (
	"testing"
)

func TestSettingsDefaultsAndSave(t *testing.T) {
	st := newTestStore(t)

	cfg, err := st.GetSettings()
	mustNoErr(t, err, "read default settings")
	if cfg.TZOffsetMin != 480 || cfg.RequestTimeoutMs != 60000 || cfg.MaxRetries != 2 {
		t.Errorf("unexpected defaults: %+v", cfg)
	}

	cfg.TZOffsetMin = -300
	cfg.MaxRetries = 5
	cfg.SampleRatePct = 10
	mustNoErr(t, st.SaveSettings(cfg), "save settings")

	got, err := st.GetSettings()
	mustNoErr(t, err, "re-read settings")
	if got.TZOffsetMin != -300 || got.MaxRetries != 5 || got.SampleRatePct != 10 {
		t.Errorf("saved settings not applied: %+v", got)
	}
	// 未写字段仍默认
	if got.RequestTimeoutMs != 60000 {
		t.Errorf("requestTimeoutMs default = %d", got.RequestTimeoutMs)
	}
}
