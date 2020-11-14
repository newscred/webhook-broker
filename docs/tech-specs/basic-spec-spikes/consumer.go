package main

import (
	"fmt"
	"io/ioutil"
	"net/http"
)

func consume(w http.ResponseWriter, r *http.Request) {
	body, err := ioutil.ReadAll(r.Body)
	if err == nil {
		fmt.Println("HOLA! " + string(body))
	}
	defer r.Body.Close()
	w.WriteHeader(204)
}
