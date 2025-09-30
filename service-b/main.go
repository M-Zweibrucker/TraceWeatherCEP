package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
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

type ViaCEPResponse struct {
	CEP         string `json:"cep"`
	Logradouro  string `json:"logradouro"`
	Complemento string `json:"complemento"`
	Bairro      string `json:"bairro"`
	Localidade  string `json:"localidade"`
	UF          string `json:"uf"`
	IBGE        string `json:"ibge"`
	GIA         string `json:"gia"`
	DDD         string `json:"ddd"`
	SIAFI       string `json:"siafi"`
	Erro        bool   `json:"erro"`
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
			semconv.ServiceName("service-b"),
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

func celsiusToFahrenheit(celsius float64) float64 {
	return celsius*1.8 + 32
}

func celsiusToKelvin(celsius float64) float64 {
	return celsius + 273
}

func getCityFromCEP(ctx context.Context, cep string) (string, error) {
	tracer := otel.Tracer("service-b")
	ctx, span := tracer.Start(ctx, "viacep-lookup")
	defer span.End()

	span.SetAttributes(
		attribute.String("cep", cep),
	)

	client := &http.Client{
		Transport: otelhttp.NewTransport(http.DefaultTransport),
		Timeout:   10 * time.Second,
	}

	url := fmt.Sprintf("https://viacep.com.br/ws/%s/json/", cep)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		span.RecordError(err)
		return "", err
	}

	resp, err := client.Do(req)
	if err != nil {
		span.RecordError(err)
		return "", err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		span.RecordError(err)
		return "", err
	}

	var viaCEPResp ViaCEPResponse
	if err := json.Unmarshal(body, &viaCEPResp); err != nil {
		span.RecordError(err)
		return "", err
	}

	if viaCEPResp.Erro {
		span.SetAttributes(attribute.Bool("cep.not_found", true))
		return "", fmt.Errorf("CEP not found")
	}

	span.SetAttributes(
		attribute.String("city", viaCEPResp.Localidade),
		attribute.String("state", viaCEPResp.UF),
	)

	return viaCEPResp.Localidade, nil
}

func getWeather(ctx context.Context, city string) (float64, error) {
	tracer := otel.Tracer("service-b")
	ctx, span := tracer.Start(ctx, "weather-lookup")
	defer span.End()

	span.SetAttributes(
		attribute.String("city", city),
	)

	client := &http.Client{
		Transport: otelhttp.NewTransport(http.DefaultTransport),
		Timeout:   10 * time.Second,
	}

	apiKey := os.Getenv("WEATHERAPI_KEY")
	if apiKey == "" {
		err := errors.New("WEATHERAPI_KEY not set")
		span.RecordError(err)
		return 0, err
	}

	weatherURL := "https://api.weatherapi.com/v1/current.json?" + url.Values{
		"key": []string{apiKey},
		"q":   []string{city},
		"aqi": []string{"no"},
	}.Encode()

	wReq, err := http.NewRequestWithContext(ctx, "GET", weatherURL, nil)
	if err != nil {
		span.RecordError(err)
		return 0, err
	}

	wResp, err := client.Do(wReq)
	if err != nil {
		span.RecordError(err)
		return 0, err
	}
	defer wResp.Body.Close()

	if wResp.StatusCode == http.StatusUnauthorized || wResp.StatusCode == http.StatusForbidden {
		err := fmt.Errorf("weatherapi auth error: %d", wResp.StatusCode)
		span.RecordError(err)
		return 0, err
	}

	wBody, err := io.ReadAll(wResp.Body)
	if err != nil {
		span.RecordError(err)
		return 0, err
	}

	var w struct {
		Current struct {
			TempC float64 `json:"temp_c"`
		} `json:"current"`
		Error *struct {
			Code int    `json:"code"`
			Msg  string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(wBody, &w); err != nil {
		span.RecordError(err)
		return 0, err
	}

	if w.Error != nil {
		if w.Error.Code == 1006 {
			err := errors.New("city not found")
			span.RecordError(err)
			return 0, err
		}
		err := fmt.Errorf("weatherapi error %d: %s", w.Error.Code, w.Error.Msg)
		span.RecordError(err)
		return 0, err
	}

	tempC := w.Current.TempC
	span.SetAttributes(
		attribute.Float64("temperature.celsius", tempC),
	)

	return tempC, nil
}

func main() {
	tp := initTracer()
	defer func() {
		if err := tp.Shutdown(context.Background()); err != nil {
			panic(err)
		}
	}()

	r := gin.Default()
	r.Use(otelgin.Middleware("service-b"))

	r.POST("/weather", func(c *gin.Context) {
		ctx := c.Request.Context()
		tracer := otel.Tracer("service-b")

		ctx, span := tracer.Start(ctx, "weather-endpoint")
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

		city, err := getCityFromCEP(ctx, req.CEP)
		if err != nil {
			if err.Error() == "CEP not found" {
				c.JSON(404, ErrorResponse{Message: "can not find zipcode"})
				return
			}
			span.RecordError(err)
			c.JSON(500, ErrorResponse{Message: "internal server error"})
			return
		}

		tempC, err := getWeather(ctx, city)
		if err != nil {
			if err.Error() == "city not found" {
				c.JSON(404, ErrorResponse{Message: "can not find zipcode"})
				return
			}
			span.RecordError(err)
			c.JSON(500, ErrorResponse{Message: "internal server error"})
			return
		}

		tempF := celsiusToFahrenheit(tempC)
		tempK := celsiusToKelvin(tempC)

		response := WeatherResponse{
			City:  city,
			TempC: tempC,
			TempF: tempF,
			TempK: tempK,
		}

		span.SetAttributes(
			attribute.String("response.city", city),
			attribute.Float64("response.temp_c", tempC),
			attribute.Float64("response.temp_f", tempF),
			attribute.Float64("response.temp_k", tempK),
		)

		c.JSON(200, response)
	})

	r.Run(":8081")
}
