# Sistema de Temperatura por CEP com OTEL e Zipkin

Este projeto implementa um sistema distribuído em Go que recebe um CEP, identifica a cidade e retorna o clima atual com temperaturas em Celsius, Fahrenheit e Kelvin. O sistema utiliza OpenTelemetry (OTEL) e Zipkin para observabilidade e tracing distribuído.

## Arquitetura

O sistema é composto por dois serviços:

- **Serviço A** (Porta 8080): Responsável pela validação do CEP e proxy para o Serviço B
- **Serviço B** (Porta 8081): Responsável pela busca do CEP, obtenção da temperatura e conversões
- **Zipkin** (Porta 9411): Coletor de traces para observabilidade

## Funcionalidades

### Serviço A
- Validação de CEP (8 dígitos)
- Proxy para Serviço B
- Retorna erro 422 para CEPs inválidos
- Implementa tracing distribuído

### Serviço B
- Busca informações do CEP via API ViaCEP
- Obtém temperatura atual via WeatherAPI (requer chave)
- Converte temperaturas: Celsius → Fahrenheit → Kelvin
- Retorna erros apropriados (404 para CEP não encontrado, 422 para formato inválido)

## APIs

### Serviço A - POST /cep
```bash
curl -X POST http://localhost:8080/cep \
  -H "Content-Type: application/json" \
  -d '{"cep": "29902555"}'
```

**Respostas:**
- `200`: Dados de temperatura da cidade
- `422`: CEP inválido (formato incorreto)
- `500`: Erro interno

### Serviço B - POST /weather
```bash
curl -X POST http://localhost:8081/weather \
  -H "Content-Type: application/json" \
  -d '{"cep": "29902555"}'
```

**Respostas:**
- `200`: `{"city": "São Paulo", "temp_C": 28.5, "temp_F": 83.3, "temp_K": 301.5}`
- `404`: CEP não encontrado
- `422`: CEP inválido
- `500`: Erro interno

## Como Executar

### Pré-requisitos
- Docker
- Docker Compose

### Execução com Docker Compose

1. Clone o repositório:
```bash
git clone <repository-url>
cd otel-cep
```

2. Defina a variável de ambiente da WeatherAPI (obrigatória para o service-b) e execute:
```bash
export WEATHERAPI_KEY=SEU_TOKEN_AQUI
docker compose up --build
```

3. Aguarde todos os serviços iniciarem (pode levar alguns minutos na primeira execução)

4. Teste o sistema:
```bash
# Teste com CEP válido
curl -X POST http://localhost:8080/cep \
  -H "Content-Type: application/json" \
  -d '{"cep": "01310100"}'

# Teste com CEP inválido
curl -X POST http://localhost:8080/cep \
  -H "Content-Type: application/json" \
  -d '{"cep": "123"}'
```

### Acessando o Zipkin

Para visualizar os traces distribuídos:
1. Abra o navegador em: http://localhost:9411
2. Execute algumas requisições
3. Visualize os traces no Zipkin UI

## Estrutura do Projeto

```
otel-cep/
├── service-a/
│   ├── main.go
│   └── Dockerfile
├── service-b/
│   ├── main.go
│   └── Dockerfile
├── go.mod
├── go.sum
├── docker-compose.yml
└── README.md
```

## Observabilidade

### Tracing Distribuído
- Cada serviço gera spans para operações importantes
- Spans são correlacionados entre serviços
- Métricas de tempo de resposta e erros

### Spans Implementados
- **Serviço A**: `validate-cep`, `call-service-b`
- **Serviço B**: `weather-endpoint`, `viacep-lookup`, `weather-lookup`

### Visualização
- Zipkin UI disponível em http://localhost:9411
- Trace completo da requisição através de ambos os serviços
- Detalhes de timing e atributos de cada span

## Desenvolvimento Local

Para desenvolvimento local sem Docker:

1. Instale as dependências:
```bash
go mod tidy
```

2. Execute o Zipkin:
```bash
docker run -d -p 9411:9411 openzipkin/zipkin:latest
```

3. Execute o Serviço B:
```bash
cd service-b
WEATHERAPI_KEY=SEU_TOKEN_AQUI go run main.go
```

4. Execute o Serviço A:
```bash
cd service-a
go run main.go
```

## Notas Importantes

- O sistema ViaCEP é usado para buscar informações do CEP
- O clima atual é obtido via WeatherAPI (`https://www.weatherapi.com/`) – requer `WEATHERAPI_KEY`
- Todos os serviços implementam middleware OTEL para tracing automático

## Variáveis de Ambiente

- `OTEL_EXPORTER_ZIPKIN_ENDPOINT` (opcional): endpoint do Zipkin exporter. Padrão no Compose: `http://zipkin:9411/api/v2/spans`. Para local sem Docker: `http://localhost:9411/api/v2/spans`.
- `WEATHERAPI_KEY` (obrigatória para `service-b`): chave da WeatherAPI. Crie em [WeatherAPI](https://www.weatherapi.com/).

## Troubleshooting

### Serviços não iniciam
- Verifique se as portas 8080, 8081 e 9411 estão livres
- Execute `docker-compose logs` para ver logs detalhados

### Erro de conexão entre serviços
- Verifique se todos os containers estão na mesma rede Docker
- Aguarde alguns segundos para os serviços iniciarem completamente

### Traces não aparecem no Zipkin
- Verifique se o Zipkin está rodando em http://localhost:9411
- Execute algumas requisições para gerar traces
- Aguarde alguns segundos para os traces serem enviados
