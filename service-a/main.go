package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"regexp"
	"time"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/contrib/instrumentation/github.com/gin-gonic/gin/otelgin"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/zipkin"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.21.0"
)

type CEPRequest struct {
	CEP string `json:"cep"`
}

type WeatherResponse struct {
	City  string  `json:"city"`
	TempC float64 `json:"temp_C"`
	TempF float64 `json:"temp_F"`
	TempK float64 `json:"temp_K"`
}

type ErrorResponse struct {
	Message string `json:"message"`
}

func initTracer() *sdktrace.TracerProvider {
	endpoint := os.Getenv("OTEL_EXPORTER_ZIPKIN_ENDPOINT")
	if endpoint == "" {
		endpoint = "http://zipkin:9411/api/v2/spans"
	}

	exporter, err := zipkin.New(endpoint)
	if err != nil {
		panic(err)
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(resource.NewWithAttributes(
			semconv.SchemaURL,
			semconv.ServiceName("service-a"),
			semconv.ServiceVersion("v1.0.0"),
		)),
	)

	otel.SetTracerProvider(tp)
	return tp
}

func validateCEP(cep string) bool {
	matched, _ := regexp.MatchString(`^\d{8}$`, cep)
	return matched
}

func main() {
	tp := initTracer()
	defer func() {
		if err := tp.Shutdown(context.Background()); err != nil {
			panic(err)
		}
	}()

	r := gin.Default()
	r.Use(otelgin.Middleware("service-a"))

	r.POST("/cep", func(c *gin.Context) {
		ctx := c.Request.Context()
		tracer := otel.Tracer("service-a")

		ctx, span := tracer.Start(ctx, "validate-cep")
		defer span.End()

		var req CEPRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			span.RecordError(err)
			c.JSON(422, ErrorResponse{Message: "invalid zipcode"})
			return
		}

		if !validateCEP(req.CEP) {
			c.JSON(422, ErrorResponse{Message: "invalid zipcode"})
			return
		}

		span.SetAttributes(
			attribute.String("cep", req.CEP),
		)

		ctx, callSpan := tracer.Start(ctx, "call-service-b")
		defer callSpan.End()

		client := &http.Client{
			Transport: otelhttp.NewTransport(http.DefaultTransport),
			Timeout:   10 * time.Second,
		}

		reqBody, _ := json.Marshal(req)
		httpReq, err := http.NewRequestWithContext(ctx, "POST", "http://service-b:8081/weather", bytes.NewBuffer(reqBody))
		if err != nil {
			callSpan.RecordError(err)
			c.JSON(500, ErrorResponse{Message: "internal server error"})
			return
		}

		httpReq.Header.Set("Content-Type", "application/json")

		resp, err := client.Do(httpReq)
		if err != nil {
			callSpan.RecordError(err)
			c.JSON(500, ErrorResponse{Message: "internal server error"})
			return
		}
		defer resp.Body.Close()

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			callSpan.RecordError(err)
			c.JSON(500, ErrorResponse{Message: "internal server error"})
			return
		}

		callSpan.SetAttributes(
			attribute.Int64("http.status_code", int64(resp.StatusCode)),
		)

		c.Data(resp.StatusCode, "application/json", body)
	})

	r.Run(":8080")
}
