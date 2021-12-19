package fingerprint

import (
	"math/cmplx"

	"github.com/mjibson/go-dsp/fft"
	"github.com/mjibson/go-dsp/window"
)

// FFT performs Fast Fourier Transform on the given signal.
func FFT(signal []float64) []float64 {
	fft_arr := fft.FFTReal(signal)
	var out []float64
	for i := 0; i < (len(fft_arr)/2)+1; i++ {
		num := 2.0 * cmplx.Abs(fft_arr[i]) / float64(len(fft_arr))
		out = append(out, num)
	}
	return out
}

// Hann applies the Hann window function on the given signal.
func Hann(signal []float64) []float64 {
	copiedSignal := make([]float64, len(signal))
	copy(copiedSignal, signal)
	window.Apply(copiedSignal, window.Hann)
	return copiedSignal
}

// Hamming applies the Hamming window function on the given signal.
func Hamming(signal []float64) []float64 {
	copiedSignal := make([]float64, len(signal))
	copy(copiedSignal, signal)
	window.Apply(copiedSignal, window.Hamming)
	return copiedSignal
}

// Bartlett applies the Bartlett window function on the given signal.
func Bartlett(signal []float64) []float64 {
	copiedSignal := make([]float64, len(signal))
	copy(copiedSignal, signal)
	window.Apply(copiedSignal, window.Bartlett)
	return copiedSignal
}

// Spectrogram returns the spectrogram of the input signal. Optional window function.
func Spectrogram(signal *Signal, winSize, hopSize int, windowFunction string) [][]float64 {
	signalSample := signal.Samples
	normalize(signalSample)
	var res [][]float64
	for i := 0; i < len(signalSample)-winSize; i += hopSize {
		switch windowFunction { // applu window if specified
		case "hann":
			res = append(res, FFT(Hann(signalSample[i:i+winSize])))
		case "hamming":
			res = append(res, FFT(Hamming(signalSample[i:i+winSize])))
		case "bartlett":
			res = append(res, FFT(Bartlett(signalSample[i:i+winSize])))
		default:
			res = append(res, FFT(signalSample[i:i+winSize]))
		}

	}
	return res
}
