package store

import (
	"errors"
	"strings"
	"testing"

	"personal-ai-gateway/internal/domain"
	"personal-ai-gateway/internal/secret"
)

func enabledPtr(b bool) *bool { return &b }

func intPtr(i int) *int { return &i }

func TestChannelCRUDAndCipher(t *testing.T) {
	st := newTestStore(t)

	key := "sk-ant-api03-secret-abcdefgh12345678"
	ch, err := st.CreateChannel(domain.ChannelInput{
		Name: "Anthropic 主", Provider: domain.ProviderAnthropic,
		BaseURL: "https://api.anthropic.com", APIKey: key,
		Priority: 1, Weight: 0,
	})
	mustNoErr(t, err, "create channel")
	if ch.ID == 0 {
		t.Fatal("channel id not assigned")
	}

	// 密文里不得出现明文
	if contains(ch.APIKeyCipher, key) {
		t.Error("ciphertext contains plaintext api key")
	}
	got, err := secret.Decrypt(ch.APIKeyCipher)
	mustNoErr(t, err, "decrypt channel key")
	mustEqual(t, got, key, "roundtrip api key")
	mustEqual(t, ch.KeyMasked, domain.MaskKey(key), "masked key")

	// 唯一名
	_, err = st.CreateChannel(domain.ChannelInput{
		Name: "Anthropic 主", Provider: domain.ProviderOpenAI, BaseURL: "x",
	})
	mustErrIs(t, err, ErrConflict, "duplicate channel name")

	// 更新名称且空 apiKey 保留原密文
	up, err := st.UpdateChannel(ch.ID, domain.ChannelInput{
		Name: "Anthropic 备", Provider: ch.Provider, BaseURL: ch.BaseURL,
		Priority: 2, TimeoutMs: 90000,
	})
	mustNoErr(t, err, "update channel")
	if up.APIKeyCipher != ch.APIKeyCipher {
		t.Error("update with empty apiKey must keep existing cipher")
	}

	// 换密钥
	key2 := "sk-ant-api03-rotated-0000"
	up2, err := st.UpdateChannel(ch.ID, domain.ChannelInput{
		Name: up.Name, Provider: up.Provider, BaseURL: up.BaseURL, APIKey: key2,
		Priority: up.Priority, TimeoutMs: up.TimeoutMs,
	})
	mustNoErr(t, err, "rotate key")
	got2, _ := secret.Decrypt(up2.APIKeyCipher)
	mustEqual(t, got2, key2, "rotated key roundtrip")

	// 启停 / 查询 / 删除
	mustNoErr(t, st.SetChannelEnabled(ch.ID, false), "disable channel")
	gotCh, err := st.GetChannel(ch.ID)
	mustNoErr(t, err, "get channel")
	if gotCh.Enabled {
		t.Error("expected disabled")
	}
	if _, err := st.GetChannelByName("Anthropic 备"); err != nil {
		t.Errorf("get by name: %v", err)
	}
	mustNoErr(t, st.DeleteChannel(ch.ID), "delete channel")
	_, err = st.GetChannel(ch.ID)
	mustErrIs(t, err, ErrNotFound, "get deleted channel")

	// ChannelModelCounts 空表返回空 map
	counts, err := st.ChannelModelCounts()
	mustNoErr(t, err, "model counts")
	mustEqual(t, len(counts), 0, "empty counts")
}

func contains(s, sub string) bool {
	return strings.Contains(s, sub)
}

func TestChannelModelCounts(t *testing.T) {
	st := newTestStore(t)
	a, _ := st.CreateChannel(domain.ChannelInput{Name: "A", Provider: domain.ProviderOpenAI, BaseURL: "http://a"})
	b, _ := st.CreateChannel(domain.ChannelInput{Name: "B", Provider: domain.ProviderOpenAI, BaseURL: "http://b"})
	m1, _ := st.CreateModel(domain.ModelInput{Name: "gpt-4o", ContextWindow: 128000})
	m2, _ := st.CreateModel(domain.ModelInput{Name: "gpt-5", ContextWindow: 400000})

	// (model, channel) 唯一,故渠道 A 挂两个不同模型凑 count=2
	mustNoErr(t, st.createOfferForTest(m1.ID, a.ID, 1), "offer a m1")
	mustNoErr(t, st.createOfferForTest(m1.ID, b.ID, 2), "offer b m1")
	mustNoErr(t, st.createOfferForTest(m2.ID, a.ID, 3), "offer a m2")

	counts, err := st.ChannelModelCounts()
	mustNoErr(t, err, "counts")
	mustEqual(t, counts[a.ID], 2, "channel a offers")
	mustEqual(t, counts[b.ID], 1, "channel b offers")
}

func (s *Store) createOfferForTest(modelID, channelID int64, priority int) error {
	_, err := s.CreateOffer(modelID, domain.OfferInput{
		ChannelID: channelID, InputPriceUsd: 1, OutputPriceUsd: 2,
		RateLimitRpm: 60, Enabled: enabledPtr(true), Priority: intPtr(priority),
	})
	return err
}

// TestDeleteChannelPurgesOrphanModels:删渠道后,失去全部供给源的模型一并删除;
// 仍有其他渠道供给源的模型保留;从未挂过供给源的孤儿模型也被清理。
func TestDeleteChannelPurgesOrphanModels(t *testing.T) {
	st := newTestStore(t)
	a, _ := st.CreateChannel(domain.ChannelInput{Name: "A", Provider: domain.ProviderOpenAI, BaseURL: "http://a"})
	b, _ := st.CreateChannel(domain.ChannelInput{Name: "B", Provider: domain.ProviderOpenAI, BaseURL: "http://b"})

	onlyA, _ := st.CreateModel(domain.ModelInput{Name: "only-a"})         // 仅挂 A → 应删
	bothAB, _ := st.CreateModel(domain.ModelInput{Name: "both-ab"})       // 挂 A+B → 保留
	onlyB, _ := st.CreateModel(domain.ModelInput{Name: "only-b"})         // 仅挂 B → 保留
	orphan, _ := st.CreateModel(domain.ModelInput{Name: "never-offered"}) // 无供给源 → 应删

	mustNoErr(t, st.createOfferForTest(onlyA.ID, a.ID, 1), "offer only-a@A")
	mustNoErr(t, st.createOfferForTest(bothAB.ID, a.ID, 1), "offer both@A")
	mustNoErr(t, st.createOfferForTest(bothAB.ID, b.ID, 2), "offer both@B")
	mustNoErr(t, st.createOfferForTest(onlyB.ID, b.ID, 1), "offer only-b@B")

	mustNoErr(t, st.DeleteChannel(a.ID), "delete channel A")

	if _, err := st.GetModel(onlyA.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("only-a should be purged, got err=%v", err)
	}
	if _, err := st.GetModel(orphan.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("orphan model should be purged, got err=%v", err)
	}
	if _, err := st.GetModel(bothAB.ID); err != nil {
		t.Errorf("both-ab should survive (has offer on B): %v", err)
	}
	offers, err := st.ListModelOffers(bothAB.ID)
	mustNoErr(t, err, "list offers both-ab")
	if len(offers) != 1 || offers[0].ChannelID != b.ID {
		t.Errorf("both-ab should keep only B offer, got %+v", offers)
	}
	if _, err := st.GetModel(onlyB.ID); err != nil {
		t.Errorf("only-b should survive: %v", err)
	}
}
