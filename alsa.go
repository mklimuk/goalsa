// Copyright 2015-2016 Cocoon Labs Ltd.
//
// See LICENSE file for terms and conditions.

// Package goalsa provides Go bindings to the ALSA library.
package goalsa

import (
	"errors"
	"fmt"
	"reflect"
	"runtime"
	"unsafe"
)

/*
#cgo LDFLAGS: -lasound
#include <alsa/asoundlib.h>
#include <stdint.h>
*/
import "C"

// Format is the type used for specifying sample formats.
type Format C.snd_pcm_format_t

// The range of sample formats supported by ALSA.
const (
	FormatS8        = C.SND_PCM_FORMAT_S8
	FormatU8        = C.SND_PCM_FORMAT_U8
	FormatS16LE     = C.SND_PCM_FORMAT_S16_LE
	FormatS16BE     = C.SND_PCM_FORMAT_S16_BE
	FormatU16LE     = C.SND_PCM_FORMAT_U16_LE
	FormatU16BE     = C.SND_PCM_FORMAT_U16_BE
	FormatS24LE     = C.SND_PCM_FORMAT_S24_LE
	FormatS24BE     = C.SND_PCM_FORMAT_S24_BE
	FormatU24LE     = C.SND_PCM_FORMAT_U24_LE
	FormatU24BE     = C.SND_PCM_FORMAT_U24_BE
	FormatS32LE     = C.SND_PCM_FORMAT_S32_LE
	FormatS32BE     = C.SND_PCM_FORMAT_S32_BE
	FormatU32LE     = C.SND_PCM_FORMAT_U32_LE
	FormatU32BE     = C.SND_PCM_FORMAT_U32_BE
	FormatFloatLE   = C.SND_PCM_FORMAT_FLOAT_LE
	FormatFloatBE   = C.SND_PCM_FORMAT_FLOAT_BE
	FormatFloat64LE = C.SND_PCM_FORMAT_FLOAT64_LE
	FormatFloat64BE = C.SND_PCM_FORMAT_FLOAT64_BE
)

const card = "default"
const mixer = "Master"

var (
	// ErrOverrun signals an overrun error
	ErrOverrun = errors.New("overrun")
	// ErrUnderrun signals an underrun error
	ErrUnderrun = errors.New("underrun")
	// ErrSize signals wrong element size
	ErrSize = errors.New("Slice element size does not correspond to the device sample size")
)

// BufferParams specifies the buffer parameters of a device.
type BufferParams struct {
	BufferFrames int
	PeriodFrames int
	Periods      int
}

type device struct {
	h            *C.snd_pcm_t
	Channels     int
	Format       Format
	Rate         int
	BufferParams BufferParams
	frames       int
}

func createError(errorMsg string, errorCode C.int) (err error) {
	strError := C.GoString(C.snd_strerror(errorCode))
	err = fmt.Errorf("%s: %s", errorMsg, strError)
	return
}

func (d *device) createDevice(deviceName string, channels int, format Format, rate int, playback bool, bufferParams BufferParams) (err error) {
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
	if ret = C.snd_pcm_hw_params_set_format(d.h, hwParams, C.snd_pcm_format_t(format)); ret < 0 {
		return createError("could not set format params", ret)
	}
	if ret = C.snd_pcm_hw_params_set_channels(d.h, hwParams, C.uint(channels)); ret < 0 {
		return createError("could not set channels params", ret)
	}
	if ret = C.snd_pcm_hw_params_set_rate(d.h, hwParams, C.uint(rate), 0); ret < 0 {
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
	var periodFrames = C.snd_pcm_uframes_t(rate / 8)
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
	d.Channels = channels
	d.Format = format
	d.Rate = rate
	d.BufferParams.BufferFrames = int(bufferSize)
	d.BufferParams.PeriodFrames = int(periodFrames)
	d.BufferParams.Periods = int(periods)
	return
}

// Close closes a device and frees the resources associated with it.
func (d *device) Close() {
	if d.h != nil {
		C.snd_pcm_drain(d.h)
		C.snd_pcm_close(d.h)
		d.h = nil
	}
	runtime.SetFinalizer(d, nil)
}

func (d device) formatSampleSize() (s int) {
	switch d.Format {
	case FormatS8, FormatU8:
		return 1
	case FormatS16LE, FormatS16BE, FormatU16LE, FormatU16BE:
		return 2
	case FormatS24LE, FormatS24BE, FormatU24LE, FormatU24BE, FormatS32LE, FormatS32BE, FormatU32LE, FormatU32BE, FormatFloatLE, FormatFloatBE:
		return 4
	case FormatFloat64LE, FormatFloat64BE:
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
	/*
		is this really required?
		if ret = C.snd_mixer_selem_register(handle, nil, nil); ret != nil {
			return createError("could not register simple element class", ret)
		}
	*/
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
		min C.long
		max C.long
	)
	if ret = C.snd_mixer_selem_get_playback_volume_range(elem, &min, &max); ret < 0 {
		return createError("could not get simple element volume range", ret)
	}
	vol := C.long(volume)
	if ret = C.snd_mixer_selem_set_playback_volume_all(elem, vol*max/100); ret < 0 {
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
	/*
		is this really required?
		if ret = C.snd_mixer_selem_register(handle, nil, nil); ret != nil {
			return createError("could not register simple element class", ret)
		}
	*/
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
func NewCaptureDevice(deviceName string, channels int, format Format, rate int, bufferParams BufferParams) (c *CaptureDevice, err error) {
	c = new(CaptureDevice)
	err = c.createDevice(deviceName, channels, format, rate, false, bufferParams)
	if err != nil {
		return nil, err
	}
	return c, nil
}

// Read reads samples into a buffer and returns the amount read.
func (c *CaptureDevice) Read(buffer interface{}) (samples int, err error) {
	bufferType := reflect.TypeOf(buffer)
	if !(bufferType.Kind() == reflect.Array ||
		bufferType.Kind() == reflect.Slice) {
		return 0, errors.New("Read requires an array type")
	}
	switch bufferType.Elem().Kind() {
	case reflect.Int8:
		if c.formatSampleSize() != 1 {
			return 0, ErrSize
		}
	case reflect.Int16:
		if c.formatSampleSize() != 2 {
			return 0, ErrSize
		}
	case reflect.Int32, reflect.Float32:
		if c.formatSampleSize() != 4 {
			return 0, ErrSize
		}
	case reflect.Float64:
		if c.formatSampleSize() != 8 {
			return 0, ErrSize
		}
	default:
		return 0, errors.New("Unsupported sample format")
	}

	val := reflect.ValueOf(buffer)
	length := val.Len()
	sliceData := val.Slice(0, length)

	var frames = C.snd_pcm_uframes_t(length / c.Channels)
	bufPtr := unsafe.Pointer(sliceData.Index(0).Addr().Pointer())

	ret := C.snd_pcm_readi(c.h, bufPtr, frames)

	if ret == -C.EPIPE {
		C.snd_pcm_prepare(c.h)
		return 0, ErrOverrun
	} else if ret < 0 {
		return 0, createError("read error", C.int(ret))
	}
	samples = int(ret) * c.Channels
	return
}

// PlaybackDevice is an ALSA device configured to playback audio.
type PlaybackDevice struct {
	device
}

// NewPlaybackDevice creates a new PlaybackDevice object.
func NewPlaybackDevice(deviceName string, channels int, format Format, rate int, bufferParams BufferParams) (p *PlaybackDevice, err error) {
	p = new(PlaybackDevice)
	err = p.createDevice(deviceName, channels, format, rate, true, bufferParams)
	if err != nil {
		return nil, err
	}
	return p, nil
}

// Write writes a buffer of data to a playback device.
func (p *PlaybackDevice) Write(buffer interface{}) (samples int, err error) {
	bufferType := reflect.TypeOf(buffer)
	if !(bufferType.Kind() == reflect.Array ||
		bufferType.Kind() == reflect.Slice) {
		return 0, errors.New("Write requires an array type")
	}
	switch bufferType.Elem().Kind() {
	case reflect.Int8:
		if p.formatSampleSize() != 1 {
			return 0, ErrSize
		}
	case reflect.Int16:
		if p.formatSampleSize() != 2 {
			return 0, ErrSize
		}
	case reflect.Int32, reflect.Float32:
		if p.formatSampleSize() != 4 {
			return 0, ErrSize
		}
	case reflect.Float64:
		if p.formatSampleSize() != 8 {
			return 0, ErrSize
		}
	default:
		return 0, errors.New("Write does not support this format")
	}

	val := reflect.ValueOf(buffer)
	length := val.Len()
	sliceData := val.Slice(0, length)

	var frames = C.snd_pcm_uframes_t(length / p.Channels)
	bufPtr := unsafe.Pointer(sliceData.Index(0).Addr().Pointer())

	ret := C.snd_pcm_writei(p.h, bufPtr, frames)
	if ret == -C.EPIPE {
		C.snd_pcm_prepare(p.h)
		return 0, ErrUnderrun
	} else if ret < 0 {
		return 0, createError("write error", C.int(ret))
	}
	samples = int(ret) * p.Channels
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
