package playback

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
)

// Converted audio never leaves the server above 48 kHz, or above what the
// device declares for the output codec; a source already within that keeps
// its own rate.
func TestConvertedAudioSampleRateIsBounded(t *testing.T) {
	picture := assets.StreamDetail{Width: 1920, Height: 1080, BitDepth: 8, DynamicRange: "sdr"}
	audio := func(codec string, channels, rate int) SourceAudio {
		return SourceAudio{Codec: codec, Channels: channels, StreamDetail: assets.StreamDetail{SampleRate: rate}}
	}
	limited := chromeProfile()
	for i := range limited.Audio {
		if limited.Audio[i].Codec == "aac" {
			limited.Audio[i].MaxSampleRate = 44100
		}
	}
	capped := chromeProfile()
	for i := range capped.Audio {
		if capped.Audio[i].Codec == "aac" {
			capped.Audio[i].MaxSampleRate = 48000
		}
	}
	for _, test := range []struct {
		name   string
		source SourceAudio
		client ClientProfile
		rate   int
	}{
		{"96 kHz lossless", audio("truehd", 6, 96000), chromeProfile(), 48000},
		{"88.2 kHz lossless", audio("dts", 6, 88200), chromeProfile(), 48000},
		{"192 kHz lossless", audio("truehd", 2, 192000), chromeProfile(), 48000},
		{"48 kHz", audio("dts", 6, 48000), chromeProfile(), 48000},
		{"44.1 kHz", audio("dts", 2, 44100), chromeProfile(), 44100},
		// A rate that was never observed cannot be kept on trust: the output is
		// given one the codec and the device both take.
		{"unknown rate", audio("dts", 2, 0), chromeProfile(), 48000},
		{"unknown rate for a device that takes up to 48 kHz", audio("dts", 2, 0), capped, 48000},
		{"unknown rate for a device that takes 44.1 kHz", audio("dts", 2, 0), limited, 44100},
		{"96 kHz in a codec the device plays to 48 kHz", audio("aac", 2, 96000), capped, 48000},
		{"48 kHz for a device that takes 44.1 kHz", audio("dts", 2, 48000), limited, 44100},
	} {
		t.Run(test.name, func(t *testing.T) {
			p := decide(t, videoSource("mkv", "h264", picture, test.source), test.client, nil)
			if p.AudioAction != "convert" {
				t.Fatalf("audio was not converted: %+v", p)
			}
			if p.AudioSampleRate != test.rate {
				t.Fatalf("planned %d Hz, want %d", p.AudioSampleRate, test.rate)
			}
			_, args, _, err := p.CodecArgs()
			if err != nil {
				t.Fatal(err)
			}
			joined := strings.Join(args, " ")
			if test.rate == 0 && strings.Contains(joined, "-ar ") {
				t.Fatalf("a rate within bounds was resampled: %s", joined)
			}
			if test.rate != 0 && !strings.Contains(joined, "-ar "+itoa(test.rate)+" ") {
				t.Fatalf("arguments do not set the output rate: %s", joined)
			}
		})
	}
	// A rate is chosen from what the output codec is encoded at and the device
	// takes. When the passthrough codec has none, the track is converted to AAC
	// instead; when no codec has one, the conversion is not planned at all: a
	// stream at a rate the device said it cannot take is never produced.
	passthrough := func(codec string, max, aacMax int) ClientProfile {
		p := appleTVProfile()
		for i := range p.Audio {
			if p.Audio[i].Codec == "aac" {
				p.Audio[i].MaxChannels, p.Audio[i].MaxSampleRate = 2, aacMax
			}
			// The device's Dolby decoders share the limit: AC-3 is the next
			// target after E-AC-3.
			if p.Audio[i].Codec == codec || p.Audio[i].Codec == "ac3" {
				p.Audio[i].MaxSampleRate = max
			}
		}
		p.AudioPassthrough = append(p.AudioPassthrough, ClientAudioPassthrough{Codec: codec, MaxChannels: 8})
		return p
	}
	tiny := chromeProfile()
	for i := range tiny.Audio {
		if tiny.Audio[i].Codec == "aac" {
			tiny.Audio[i].MaxSampleRate = 4000
		}
	}
	for _, test := range []struct {
		name   string
		source SourceAudio
		client ClientProfile
		codec  string
		rate   int
	}{
		{"96 kHz to E-AC-3", audio("truehd", 6, 96000), passthrough("eac3", 0, 0), "eac3", 48000},
		{"96 kHz to E-AC-3 for a device that takes 44.1 kHz", audio("truehd", 6, 96000), passthrough("eac3", 44100, 0), "eac3", 44100},
		{"22.05 kHz to E-AC-3, which has no such rate", audio("dts", 6, 22050), passthrough("eac3", 0, 0), "eac3", 32000},
		{"96 kHz when E-AC-3 has no rate under the device's limit", audio("truehd", 6, 96000), passthrough("eac3", 24000, 0), "aac", 48000},
		{"48 kHz when E-AC-3 has no rate under the device's limit", audio("dts", 6, 48000), passthrough("eac3", 24000, 0), "aac", 48000},
		{"a rate AAC is not encoded at", audio("dts", 2, 37800), chromeProfile(), "aac", 44100},
		{"unknown rate to E-AC-3", audio("truehd", 6, 0), passthrough("eac3", 0, 0), "eac3", 48000},
		{"unknown rate when E-AC-3 has no rate under the device's limit", audio("truehd", 6, 0), passthrough("eac3", 24000, 0), "aac", 48000},
	} {
		t.Run(test.name, func(t *testing.T) {
			p := decide(t, videoSource("mkv", "h264", picture, test.source), test.client, nil)
			if p.AudioAction != "convert" || p.OutputAudioCodec != test.codec {
				t.Fatalf("audio is %s to %s, want a conversion to %s", p.AudioAction, p.OutputAudioCodec, test.codec)
			}
			if p.AudioSampleRate != test.rate {
				t.Fatalf("planned %d Hz, want %d", p.AudioSampleRate, test.rate)
			}
			if _, _, _, err := p.CodecArgs(); err != nil {
				t.Fatal(err)
			}
		})
	}
	for _, test := range []struct {
		name   string
		source *DeliverySource
		client ClientProfile
	}{
		{"a device limit below every AAC rate", videoSource("mkv", "h264", picture, audio("dts", 2, 48000)), tiny},
		{"a device limit below every AAC rate, source rate unknown", videoSource("mkv", "h264", picture, audio("dts", 2, 0)), tiny},
		{"no codec with a rate under the device's limits, source rate unknown", videoSource("mkv", "h264", picture, audio("truehd", 6, 0)), passthrough("eac3", 24000, 4000)},
		{"no codec with a rate under the device's limits", videoSource("mkv", "h264", picture, audio("truehd", 6, 96000)), passthrough("eac3", 24000, 4000)},
		{"a picture that is converted too", videoSource("mkv", "vp9", picture, audio("dts", 2, 48000)), func() ClientProfile {
			p := appleTVProfile()
			for i := range p.Audio {
				if p.Audio[i].Codec == "aac" {
					p.Audio[i].MaxSampleRate = 4000
				}
			}
			return p
		}()},
	} {
		t.Run(test.name, func(t *testing.T) {
			policy := openPolicy()
			p, err := planDelivery(DeliveryInput{Source: test.source, Client: test.client, Policy: policy, Target: autoTarget(policy, 1080), Config: DefaultDeliveryConfiguration(), TranscodingEnabled: true})
			var refused ErrDeliveryRefused
			if !errors.As(err, &refused) {
				_, args, _, _ := p.CodecArgs()
				t.Fatalf("a conversion the device cannot take was planned: %s to %s at %d Hz, arguments %v (%v)", p.AudioAction, p.OutputAudioCodec, p.AudioSampleRate, args, err)
			}
			for _, route := range p.Trace.Routes {
				if route.Admissible {
					t.Fatalf("route %s is admissible: %+v", route.Route, route)
				}
			}
			if !rejected(p, DeliveryVideoConversion, RejectAudioSampleRate) {
				t.Fatalf("the refusal does not say why: %+v", p.Trace.Routes)
			}
		})
	}
	t.Run("alternate renditions", func(t *testing.T) {
		p := decide(t, videoSource("mkv", "h264", picture, audio("truehd", 6, 96000), audio("dts", 2, 44100)), chromeProfile(), nil)
		if len(p.Renditions) != 2 || p.Renditions[0].Action != "convert" || p.Renditions[0].SampleRate != 48000 || p.Renditions[1].SampleRate != 44100 {
			t.Fatalf("renditions %+v", p.Renditions)
		}
	})
	t.Run("an alternate rendition the device cannot take is not offered", func(t *testing.T) {
		limited := chromeProfile()
		for i := range limited.Audio {
			if limited.Audio[i].Codec == "aac" {
				limited.Audio[i].MaxSampleRate = 4000
			}
		}
		// The first track is AAC at a rate the device takes; the others would
		// have to be converted to AAC, which has no rate under 4 kHz.
		p := decide(t, videoSource("mkv", "h264", picture, audio("aac", 2, 4000), audio("dts", 2, 48000), audio("dts", 2, 44100)), limited, nil)
		for _, r := range p.Renditions {
			if r.Action == "convert" {
				t.Fatalf("a rendition was planned for conversion: %+v", p.Renditions)
			}
		}
	})
}

// The planned rate is part of the stored plan: every later production window
// of the session converts at it.
func TestConvertedAudioSampleRateIsStoredWithThePlan(t *testing.T) {
	db, s, p, _, item, _ := activityFixture(t)
	s.ConfigureHLS(&HLS{})
	asset := firstCatalogAssetToken(t, db)
	updateCatalogAsset(t, db, asset, func(a *compactcatalog.Asset) { a.Container, a.AudioCodec = "mkv", "truehd" })
	mustAudioExec(t, db, `INSERT INTO asset_stream_facts(asset_id,revision,size,modified_ns,fingerprint,detail_version) VALUES(?,1,1,1,'test',?)`, asset, assets.StreamDetailVersion)
	mustAudioExec(t, db, `INSERT INTO asset_streams(asset_id,stream_index,type,codec,language,title,channels,channel_layout,is_default,is_forced,detail_json) VALUES(?,0,'video','h264','','',0,'',1,0,'{"width":640,"height":360,"bitDepth":8}'),(?,1,'audio','truehd','eng','',6,'5.1',1,0,'{"sampleRate":96000}')`, asset, asset)
	session, err := s.Create(p, item, "automatic", "high-rate")
	if err != nil {
		t.Fatal(err)
	}
	stored, err := loadDeliveryPlan(context.Background(), db, session.ID)
	if err != nil || stored == nil || stored.AudioAction != "convert" || stored.AudioSampleRate != 48000 || stored.Policy == nil {
		t.Fatalf("stored plan %+v %v", stored, err)
	}
	_, args, _, err := stored.CodecArgs()
	if err != nil || !strings.Contains(strings.Join(args, " "), "-ar 48000 ") {
		t.Fatalf("stored plan arguments %v %v", args, err)
	}
}
