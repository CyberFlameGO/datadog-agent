package main

import (
	"context"

	ddlambda "github.com/DataDog/datadog-lambda-go"
	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
)

type testResponse struct {
	StatusCode int    `json:"statusCode"`
	Body       string `json:"body"`
}

func testHandler(ctx context.Context, ev events.APIGatewayProxyRequest) (testResponse, error) {
	ddlambda.Metric("serverless.lambda-extension.integration-test.count", 1.0)
	return testResponse{
		StatusCode: 200,
		Body:       "ok",
	}, nil
}

func main() {
	lambda.Start(ddlambda.WrapHandler(testHandler, nil))
}
