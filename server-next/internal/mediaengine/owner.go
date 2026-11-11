package mediaengine

import (
	"github.com/PorticoMediaServer/portico/server-next/internal/decoder"
	"github.com/PorticoMediaServer/portico/server-next/internal/playback"
	"github.com/PorticoMediaServer/portico/server-next/internal/settings"
	"github.com/PorticoMediaServer/portico/server-next/internal/streaming"
)

// The owner's Settings › Streaming, as the planner and the streams read them. They are read
// at every plan and at every conversion's admission, so a change applies to the next play
// with no restart. A server on which nothing was ever saved has the document's defaults,
// which plan exactly as the engine did before it read any settings.

// owner is the owner's streaming settings as they are now.
func (e *Engine) owner() settings.StreamingSettings { return settings.Streaming.Get(e.settings) }

// Delivery is the owner's settings in the stream runtime's terms (streaming.DeliverySettings).
func (e *Engine) Delivery() streaming.DeliveryConfiguration { return delivery(e.owner()) }

// delivery maps each streaming setting onto the one field of the runtime's configuration it
// decides. Everything else keeps the runtime's default.
func delivery(owner settings.StreamingSettings) streaming.DeliveryConfiguration {
	c := streaming.DefaultDeliveryConfiguration()
	switch owner.ConvertWhenNeeded {
	case settings.ConvertSoundOnly:
		c.ConversionMode = "sound"
	case settings.ConvertNever:
		c.ConversionMode = "never"
	default:
		c.ConversionMode = "all"
	}
	switch owner.GraphicsDevice {
	case "automatic", "":
	case "processor":
		c.HardwareBackend, c.HardwareSelection = string(decoder.BackendSoftware), string(decoder.BackendSoftware)
	default:
		c.HardwareBackend = owner.GraphicsDevice
	}
	c.SoftwareDecode = !owner.HardwareDecoding
	c.MaxConversions = owner.ConversionsAtOnce
	c.ToneMapping = owner.ToneMapping
	return c.Normalized()
}

// applyConversion puts the owner's conversion folder and FFmpeg program in force. One that
// cannot be used is not: what is in use stays, and the reason is logged for the owner.
func (e *Engine) applyConversion(owner settings.StreamingSettings) {
	if err := e.SetConversion(owner.ConversionFolder, owner.FFmpegProgram); err != nil {
		e.log.Error("the conversion folder or FFmpeg program in Settings could not be used, so the ones in use stay", "error", err)
	}
}

// connection says how the request reached the server: from home or away, and on mobile data
// when the app said so. The app's language is what "the app's language" in the viewer's
// preferred languages stands for.
func connection(c *streaming.DeliveryContext, r playback.PlanRequest) {
	c.NetworkClass, c.AppLanguage = streaming.NetworkRemote, r.AppLanguage
	switch r.Network {
	case playback.NetworkLocal:
		c.NetworkClass = streaming.NetworkLocal
	case playback.NetworkCellular:
		c.TransportClass = "cellular"
	}
}
