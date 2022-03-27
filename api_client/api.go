package main

import (
	"bytes"
	"image/jpeg"
	"log"
	"net/http"
	"strconv"

	"github.com/gorilla/mux"
)

func setupApi() {
	r := mux.NewRouter()
	r.HandleFunc("/getMapData", getMapData).Methods("GET")
	http.Handle("/", r)
	go http.ListenAndServe(":8000", r)
}

func getMapData(res http.ResponseWriter, req *http.Request) {
	img := getImageFromMapGrid()
	buffer := new(bytes.Buffer)
	if err := jpeg.Encode(buffer, img, nil); err != nil {
		log.Println("unable to encode image.")
	}

	res.Header().Set("Content-Type", "image/jpeg")
	res.Header().Set("Content-Length", strconv.Itoa(len(buffer.Bytes())))
	if _, err := res.Write(buffer.Bytes()); err != nil {
		log.Println("unable to write image.")
	}
}
