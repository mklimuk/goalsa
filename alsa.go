// Copyright 2015-2016 Cocoon Labs Ltd.
//
// See LICENSE file for terms and conditions.

// Package goalsa provides Go bindings to the ALSA library.
package goalsa

import (
	"errors"
	"fmt"
	"runtime"
	"unsafe"
)

/*
#cgo LDFLAGS: -lasound
#include <alsa/asoundlib.h>
#include <stdint.h>
*/
import "C"

// SampleFormat is the type used for specifying sample formats.
type SampleFormat C.snd_pcm_format_t

// The range of sample formats supported by ALSA.
const (
	SampleFormatS8        = C.SND_PCM_FORMAT_S8
	SampleFormatU8        = C.SND_PCM_FORMAT_U8
	SampleFormatS16LE     = C.SND_PCM_FORMAT_S16_LE
	SampleFormatS16BE     = C.SND_PCM_FORMAT_S16_BE
	SampleFormatU16LE     = C.SND_PCM_FORMAT_U16_LE
	SampleFormatU16BE     = C.SND_PCM_FORMAT_U16_BE
	SampleFormatS24LE     = C.SND_PCM_FORMAT_S24_LE
	SampleFormatS24BE     = C.SND_PCM_FORMAT_S24_BE
	SampleFormatU24LE     = C.SND_PCM_FORMAT_U24_LE
	SampleFormatU24BE     = C.SND_PCM_FORMAT_U24_BE
	SampleFormatS32LE     = C.SND_PCM_FORMAT_S32_LE
	SampleFormatS32BE     = C.SND_PCM_FORMAT_S32_BE
	SampleFormatU32LE     = C.SND_PCM_FORMAT_U32_LE
	SampleFormatU32BE     = C.SND_PCM_FORMAT_U32_BE
	SampleFormatFloatLE   = C.SND_PCM_FORMAT_FLOAT_LE
	SampleFormatFloatBE   = C.SND_PCM_FORMAT_FLOAT_BE
	SampleFormatFloat64LE = C.SND_PCM_FORMAT_FLOAT64_LE
	SampleFormatFloat64BE = C.SND_PCM_FORMAT_FLOAT64_BE
)

const card = "default"
const mixer = "PCM"

var (
	// ErrOverrun signals an overrun error
	ErrOverrun = errors.New("overrun")
	// ErrUnderrun signals an underrun error
	ErrUnderrun = errors.New("underrun")
)

// BufferParams specify the buffer parameters of a device.
type BufferParams struct {
	BufferFrames int
	PeriodFrames int
	Periods      int
}

//AudioParams describe audio data parameters expected by the device.
type AudioParams struct {
	Channels     int
	SamplingRate int
	SampleFormat SampleFormat
}

type device struct {
	h            *C.snd_pcm_t
	Audio        *AudioParams
	BufferParams BufferParams
	frames       int
}

func createError(errorMsg string, errorCode C.int) (err error) {
	strError := C.GoString(C.snd_strerror(errorCode))
	err = fmt.Errorf("%s: %s", errorMsg, strError)
	return
}

func (d *device) initDevice(deviceName string, audio *AudioParams, playback bool, bufferParams BufferParams) (err error) {
	deviceCString := C.CString(deviceName)
	defer C.free(unsafe.Pointer(deviceCString))
	var ret C.int
	if playback {
		ret = C.snd_pcm_open(&d.h, deviceCString, C.SND_PCM_STREAM_PLAYBACK, 0)
	} else {
		ret = C.snd_pcm_open(&d.h, deviceCString, C.SND_PCM_STREAM_CAPTURE, 0)
	}
	if ret < 0 {
		return fmt.Errorf("could not open ALSA device %s", deviceName)
	}
	runtime.SetFinalizer(d, (*device).Close)
	var hwParams *C.snd_pcm_hw_params_t
	if ret = C.snd_pcm_hw_params_malloc(&hwParams); ret < 0 {
		return createError("could not alloc hw params", ret)
	}
	defer C.snd_pcm_hw_params_free(hwParams)
	if ret = C.snd_pcm_hw_params_any(d.h, hwParams); ret < 0 {
		return createError("could not set default hw params", ret)
	}
	if ret = C.snd_pcm_hw_params_set_access(d.h, hwParams, C.SND_PCM_ACCESS_RW_INTERLEAVED); ret < 0 {
		return createError("could not set access params", ret)
	}
	if ret = C.snd_pcm_hw_params_set_format(d.h, hwParams, C.snd_pcm_format_t(audio.SampleFormat)); ret < 0 {
		return createError("could not set format params", ret)
	}
	if ret = C.snd_pcm_hw_params_set_channels(d.h, hwParams, C.uint(audio.Channels)); ret < 0 {
		return createError("could not set channels params", ret)
	}
	if ret = C.snd_pcm_hw_params_set_rate(d.h, hwParams, C.uint(audio.SamplingRate), 0); ret < 0 {
		return createError("could not set rate params", ret)
	}
	var bufferSize = C.snd_pcm_uframes_t(bufferParams.BufferFrames)
	if bufferParams.BufferFrames == 0 {
		// Default buffer size: max buffer size
		if ret = C.snd_pcm_hw_params_get_buffer_size_max(hwParams, &bufferSize); ret < 0 {
			return createError("could not get buffer size", ret)
		}
	}
	if ret = C.snd_pcm_hw_params_set_buffer_size_near(d.h, hwParams, &bufferSize); ret < 0 {
		return createError("could not set buffer size", ret)
	}
	// Default period size: 1/8 of a second
	var periodFrames = C.snd_pcm_uframes_t(audio.SamplingRate / 8)
	if bufferParams.PeriodFrames > 0 {
		periodFrames = C.snd_pcm_uframes_t(bufferParams.PeriodFrames)
	} else if bufferParams.Periods > 0 {
		periodFrames = C.snd_pcm_uframes_t(int(bufferSize) / bufferParams.Periods)
	}
	if ret = C.snd_pcm_hw_params_set_period_size_near(d.h, hwParams, &periodFrames, nil); ret < 0 {
		return createError("could not set period size", ret)
	}
	var periods = C.uint(0)
	if ret = C.snd_pcm_hw_params_get_periods(hwParams, &periods, nil); ret < 0 {
		return createError("could not get periods", ret)
	}
	if ret = C.snd_pcm_hw_params(d.h, hwParams); ret < 0 {
		return createError("could not set hw params", ret)
	}
	d.frames = int(periodFrames)
	d.Audio = audio
	d.BufferParams.BufferFrames = int(bufferSize)
	d.BufferParams.PeriodFrames = int(periodFrames)
	d.BufferParams.Periods = int(periods)
	return
}

// Close closes a device and frees the resources associated with it.
func (d *device) Close() error {
	if d.h != nil {
		C.snd_pcm_drain(d.h)
		ret := C.snd_pcm_close(d.h)
		if ret < 0 {
			return createError("Error closing device handle", ret)
		}
		d.h = nil
	}
	runtime.SetFinalizer(d, nil)
	return nil
}

func (d device) formatSampleSize() (s int) {
	switch d.Audio.SampleFormat {
	case SampleFormatS8, SampleFormatU8:
		return 1
	case SampleFormatS16LE, SampleFormatS16BE, SampleFormatU16LE, SampleFormatU16BE:
		return 2
	case SampleFormatS24LE, SampleFormatS24BE, SampleFormatU24LE, SampleFormatU24BE, SampleFormatS32LE, SampleFormatS32BE, SampleFormatU32LE, SampleFormatU32BE, SampleFormatFloatLE, SampleFormatFloatBE:
		return 4
	case SampleFormatFloat64LE, SampleFormatFloat64BE:
		return 8
	}
	panic("unsupported format")
}

func (d *device) SetMasterVolume(volume int) error {
	var (
		handle *C.snd_mixer_t
		ret    C.int
	)
	if ret = C.snd_mixer_open(&handle, 0); ret < 0 {
		return createError("could not open mixer", ret)
	}
	defer C.snd_mixer_close(handle)

	c := C.CString(card)
	defer C.free(unsafe.Pointer(c))
	C.snd_mixer_attach(handle, c)

	if ret = C.snd_mixer_selem_register(handle, nil, nil); ret < 0 {
		return createError("could not register simple element class", ret)
	}

	if ret = C.snd_mixer_load(handle); ret < 0 {
		return createError("could not load mixer handle", ret)
	}

	//sid is a mixer simple element identifier
	var sid *C.snd_mixer_selem_id_t
	if ret = C.snd_mixer_selem_id_malloc(&sid); ret < 0 {
		return createError("could not allocate simple element pointer", ret)
	}
	defer C.snd_mixer_selem_id_free(sid)

	C.snd_mixer_selem_id_set_index(sid, 0)
	m := C.CString(mixer)
	defer C.free(unsafe.Pointer(m))
	C.snd_mixer_selem_id_set_name(sid, m)

	// getting the mixer line
	var elem *C.snd_mixer_elem_t
	elem = C.snd_mixer_find_selem(handle, sid)
	var (
		min   C.long
		max   C.long
		total C.long
	)
	if ret = C.snd_mixer_selem_get_playback_volume_range(elem, &min, &max); ret < 0 {
		return createError("could not get simple element volume range", ret)
	}
	vol := C.long(volume)
	total = max - min
	vol = vol*total/100 + min
	if ret = C.snd_mixer_selem_set_playback_volume_all(elem, vol); ret < 0 {
		return createError("could not set playback volume", ret)
	}
	return nil
}

func (d *device) GetMasterVolume() (int, error) {
	var (
		handle *C.snd_mixer_t
		ret    C.int
	)
	if ret = C.snd_mixer_open(&handle, 0); ret < 0 {
		return 0, createError("could not open mixer", ret)
	}
	defer C.snd_mixer_close(handle)

	c := C.CString(card)
	defer C.free(unsafe.Pointer(c))
	C.snd_mixer_attach(handle, c)

	if ret = C.snd_mixer_selem_register(handle, nil, nil); ret < 0 {
		return 0, createError("could not register simple element class", ret)
	}

	if ret = C.snd_mixer_load(handle); ret < 0 {
		return 0, createError("could not load mixer handle", ret)
	}

	//sid is a mixer simple element identifier
	var sid *C.snd_mixer_selem_id_t
	if ret = C.snd_mixer_selem_id_malloc(&sid); ret < 0 {
		return 0, createError("could not allocate simple element pointer", ret)
	}
	defer C.snd_mixer_selem_id_free(sid)

	C.snd_mixer_selem_id_set_index(sid, 0)
	m := C.CString(mixer)
	defer C.free(unsafe.Pointer(m))
	C.snd_mixer_selem_id_set_name(sid, m)

	// getting the mixer line
	var elem *C.snd_mixer_elem_t
	elem = C.snd_mixer_find_selem(handle, sid)
	var (
		min    C.long
		max    C.long
		outvol C.long
	)
	if ret = C.snd_mixer_selem_get_playback_volume_range(elem, &min, &max); ret < 0 {
		return 0, createError("could not get simple element volume range", ret)
	}

	if ret = C.snd_mixer_selem_get_playback_volume(elem, C.SND_MIXER_SCHN_MONO, &outvol); ret < 0 {
		return 0, createError("could not get playback volume", ret)
	}
	outvol -= min
	max -= min
	outvol = 100 * outvol / max
	return int(outvol), nil
}

// CaptureDevice is an ALSA device configured to record audio.
type CaptureDevice struct {
	device
}

// NewCaptureDevice creates a new CaptureDevice object.
func NewCaptureDevice(deviceName string, audio *AudioParams, bufferParams BufferParams) (c *CaptureDevice, err error) {
	c = new(CaptureDevice)
	err = c.initDevice(deviceName, audio, false, bufferParams)
	if err != nil {
		return nil, err
	}
	return c, nil
}

// Read reads samples into a buffer and returns the amount read.
func (c *CaptureDevice) Read(buffer []byte) (samples int, err error) {
	var frames = C.snd_pcm_uframes_t(len(buffer) / c.Audio.Channels)
	ret := C.snd_pcm_readi(c.h, unsafe.Pointer(&buffer[0]), frames)
	if ret == -C.EPIPE {
		C.snd_pcm_prepare(c.h)
		return 0, ErrOverrun
	} else if ret < 0 {
		return 0, createError("read error", C.int(ret))
	}
	samples = int(ret) * c.Audio.Channels
	return
}

// PlaybackDevice is an ALSA device configured to playback audio.
type PlaybackDevice struct {
	device
}

// NewPlaybackDevice creates a new PlaybackDevice object.
func NewPlaybackDevice(deviceName string, audio *AudioParams, bufferParams BufferParams) (p *PlaybackDevice, err error) {
	p = new(PlaybackDevice)
	err = p.initDevice(deviceName, audio, true, bufferParams)
	if err != nil {
		return nil, err
	}
	return p, nil
}

// Write writes a buffer of data to a playback device.
func (p *PlaybackDevice) Write(buffer []byte) (samples int, err error) {
	var frames = C.snd_pcm_uframes_t(len(buffer) / p.Audio.Channels)
	ret := C.snd_pcm_writei(p.h, unsafe.Pointer(&buffer[0]), frames)
	if ret == -C.EPIPE {
		// this allows us to write back to the device after underrun
		C.snd_pcm_prepare(p.h)
		return 0, ErrUnderrun
	} else if ret < 0 {
		return 0, createError("write error", C.int(ret))
	}
	samples = int(ret) * p.Audio.Channels
	return
}

// Drop stream, this function stops the PCM immediately.
// The pending samples on the buffer are ignored.
func (p *PlaybackDevice) Drop() error {
	var ret C.int
	if ret = C.snd_pcm_drop(p.h); ret < 0 {
		return createError("Could not drop the stream", ret)
	}
	return nil
}
