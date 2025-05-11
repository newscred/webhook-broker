package main

import (
	"fmt"
	"io"
	"net/http"
)

func consume(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err == nil {
		fmt.Println("HOLA! " + string(body))
	}
	defer r.Body.Close()
	w.WriteHeader(204)
}
