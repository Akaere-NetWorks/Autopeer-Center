package whois

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type whoisField struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type whoisResponse struct {
	Success bool         `json:"success"`
	Fields  []whoisField `json:"fields"`
}

func LookupASN(ip string) int64 {
	if ip == "" {
		return 0
	}

	client := &http.Client{Timeout: 10 * time.Second}
	url := fmt.Sprintf("https://whois.akae.re/api/whois?q=%s", ip)

	resp, err := client.Get(url)
	if err != nil {
		return 0
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err != nil {
		return 0
	}

	var result whoisResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return 0
	}

	if !result.Success {
		return 0
	}

	routeFound := false
	for _, f := range result.Fields {
		if f.Name == "route" {
			routeFound = true
		}
		if routeFound && f.Name == "origin" {
			asnStr := strings.TrimPrefix(f.Value, "AS")
			asn, err := strconv.ParseInt(asnStr, 10, 64)
			if err == nil {
				return asn
			}
			routeFound = false
		}
	}

	return 0
}
