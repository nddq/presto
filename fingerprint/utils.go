package fingerprint

func convertToBinary(signal []float64, peaks []int) []int {
	res := make([]int, len(signal))
	for i := range signal {
		if isInSlice(i, peaks) {
			res[i] = 1
		} else {
			res[i] = 0
		}
	}
	return res
}

func isInSlice(elem int, slice []int) bool {
	for _, i := range slice {
		if elem == i {
			return true
		}
	}
	return false
}

func max(ar []float64) float64 {
	max := ar[0]
	for _, v := range ar {
		if v > max {
			max = v
		}
	}
	return max
}

func normalize(signal []float64) {
	max := max(signal)
	for i := range signal {
		signal[i] = signal[i] / max
	}
}
