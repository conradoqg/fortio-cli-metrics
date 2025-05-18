# Makefile for running RabbitMQ with limited resources and benchmarking with Go

.PHONY: build-fortio-cli-metrics

build-fortio-cli-metrics:
	@echo "Building fortio-cli-metrics..."
	@mkdir -p output
	@cd cmd/fortio-cli-metrics && go build -o ../../output/fortio-cli-metrics .