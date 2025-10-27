package main

import (
	"os"

	"github.com/stripe/stripe-go/v72/client"
)

func NewStripeClient() *client.API {
	apiKey := os.Getenv("STRIPE_API_KEY")
	sc := &client.API{}
	sc.Init(apiKey, nil)
	return sc
}
