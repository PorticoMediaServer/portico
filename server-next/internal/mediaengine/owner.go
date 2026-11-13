package mediaengine

import (
	"github.com/PorticoMediaServer/portico/server-next/internal/decoder"
	"github.com/PorticoMediaServer/portico/server-next/internal/playback"
	"github.com/PorticoMediaServer/portico/server-next/internal/settings"
	"github.com/PorticoMediaServer/portico/server-next/internal/streaming"
)

// The owner's Settings › Streaming, as the planner and the streams read them. They are read
// at every plan and at every conversion's admission, so a change applies to the next play
// with no restart. A server with nothing saved uses the document's defaults.

// owner is the owner's streaming settings as they are now.
func (e *Engine) owner() settings.StreamingSettings { return settings.Streaming.Get(e.settings) }

// shareDeviceSetting lets the conversion-device setting reach every job made from FFmpeg's
// facts, not only a play: with software encoding chosen no thumbnail, preview or linear output
// uses the graphics device either. It is read as it is now each time, like every other
// streaming setting.
func (e *Engine) shareDeviceSetting() {
	decoder.SetSoftwareEncodingChosen(func() bool { return softwareEncoding(e.owner()) })
}

// softwareEncoding reports the conversion-device setting "software encoding" (stored as
// "processor"): conversions run on the processor and no graphics device is used.
func softwareEncoding(owner settings.StreamingSettings) bool {
	return owner.GraphicsDevice == "processor"
}

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
		if decoder.ValidBackend(owner.GraphicsDevice) {
			c.HardwareBackend = owner.GraphicsDevice
		} else {
			c.HardwareSelection = owner.GraphicsDevice
		}
	}
	c.SoftwareDecode = !owner.HardwareDecoding
	c.MaxConversions = owner.ConversionsAtOnce
	c.MaxSoftwareConversions = owner.ProcessorConversionsAtOnce
	c.MaxHardwareConversions = owner.HardwareConversionsAtOnce
	c.TranscodeLocation = "disk"
	if owner.ConversionLocation == "memory" {
		c.TranscodeLocation = "memory"
	}
	c.ToneMapping = owner.ToneMapping
	c.SoftwarePreset = owner.EncoderSpeed
	c.PlanningPolicy = owner.PlanningPolicy
	c.RemuxEnabled = owner.Repackaging
	c.OutputFormat = owner.ConversionFormat
	c.ThrottleBufferSeconds = owner.ConvertAheadSeconds
	c.PlayedRetentionSeconds = owner.KeepForRewindingSeconds
	c.SessionStorageMiB = owner.SpacePerStreamMiB
	c.MemoryCap = owner.MemoryLimit
	c.ToneMapAlgorithm = owner.ToneMappingMethod
	return c.Normalized()
}

// applyConversion puts the owner's conversion folder and FFmpeg program, as they are saved
// now, in force. It runs beside the save, because a new program is tried out first and that
// takes seconds. One that cannot be used is not put in force: what is in use stays, and the
// owner is told why on the settings page itself (the document's problem line, read back with
// the document) as well as in the server log.
func (e *Engine) applyConversion() {
	// The settings are read once the switch before this one is over: of saves close together
	// the last one saved is the last one put in force.
	e.switching.Lock()
	defer e.switching.Unlock()
	owner := e.owner()
	folder := owner.ConversionFolder
	if owner.ConversionLocation != "folder" {
		folder = ""
	}
	err := e.setConversion(folder, owner.FFmpegProgram)
	if err != nil {
		e.log.Error("the conversion folder or FFmpeg program in Settings could not be used, so the ones in use stay", "error", err)
		settings.Streaming.Report(e.settings, "This is not in use: "+err.Error()+".")
		return
	}
	if owner.ConversionLocation == "memory" {
		e.mu.Lock()
		memory, disk := e.current.streams.MemoryFolder(), e.current.folder
		e.mu.Unlock()
		if memory == "" {
			settings.Streaming.Report(e.settings, "Memory output is not available; conversions use "+disk+".")
			return
		}
	}
	settings.Streaming.Report(e.settings, "")
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
