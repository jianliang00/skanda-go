package skanda

import "fmt"

func ExampleCompress() {
	input := []byte("skanda example payload")
	compressed, err := Compress(input, WithLevel(2), WithDecSpeedBias(0.5))
	if err != nil {
		panic(err)
	}
	output, err := Decompress(compressed, len(input))
	if err != nil {
		panic(err)
	}
	fmt.Println(string(output))
	// Output: skanda example payload
}

func ExampleDecode() {
	input := []byte("caller-owned destination buffer")
	compressed, err := Compress(input)
	if err != nil {
		panic(err)
	}
	output := make([]byte, len(input))
	if err := Decode(output, compressed); err != nil {
		panic(err)
	}
	fmt.Println(string(output))
	// Output: caller-owned destination buffer
}
