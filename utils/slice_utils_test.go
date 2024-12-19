package utils

import (
	"reflect"
	"testing"
)

func TestDeleteFromSlice(t *testing.T) {
	t.Parallel()

	type args[T any] struct {
		slice []T
		index int
	}
	tests := []struct {
		name string
		args args[int]
		want []int
	}{
		{
			name: "ValidIndex",
			args: args[int]{
				slice: []int{1, 2, 3, 4, 5},
				index: 2,
			},
			want: []int{1, 2, 4, 5},
		},
		{
			name: "NegativeIndex",
			args: args[int]{
				slice: []int{1, 2, 3, 4, 5},
				index: -1,
			},
			want: []int{1, 2, 3, 4, 5}, // Original slice
		},
		{
			name: "IndexOutOfRange",
			args: args[int]{
				slice: []int{1, 2, 3, 4, 5},
				index: 5,
			},
			want: []int{1, 2, 3, 4, 5}, // Original slice
		},
		{
			name: "EmptySlice",
			args: args[int]{
				slice: []int{},
				index: 0,
			},
			want: []int{}, // Empty slice
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := DeleteFromSlice(tt.args.slice, tt.args.index); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("DeleteFromSlice() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestFindIndex(t *testing.T) {
	t.Parallel()

	type args struct {
		slice []string
		value string
	}
	tests := []struct {
		name string
		args args
		want int
	}{
		{
			name: "ValuePresent",
			args: args{
				slice: []string{"apple", "banana", "cherry"},
				value: "banana",
			},
			want: 1,
		},
		{
			name: "ValueNotPresent",
			args: args{
				slice: []string{"apple", "banana", "cherry"},
				value: "grape",
			},
			want: -1,
		},
		{
			name: "EmptySlice",
			args: args{
				slice: []string{},
				value: "apple",
			},
			want: -1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := FindIndex(tt.args.slice, tt.args.value); got != tt.want { // corrected the test case, changed to tt.args.value
				t.Errorf("FindIndex() = %v, want %v", got, tt.want)
			}
		})
	}
}
