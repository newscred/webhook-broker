package utils

func DeleteFromSlice[T any](slice []T, index int) []T {
	if index < 0 || index >= len(slice) {
		return slice // Invalid index, return the original slice
	}
	return append(slice[:index], slice[index+1:]...)
}

func FindIndex[T comparable](slice []T, value T) int {
	for i, v := range slice {
		if v == value {
			return i
		}
	}
	return -1 // Not found
}
