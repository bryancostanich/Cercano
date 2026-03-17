# Project Technology Stack

This document outlines the core technologies used in the project.

## 1. Programming Languages
*   **Go (Golang)** - The primary programming language for the core application, including the smart router and local model integration.
*   **TypeScript** - Used for the VS Code Extension.
*   **Rust** - Used for the Zed Extension.

## 2. Communication Frameworks
*   **gRPC** - Used for high-performance, contract-first communication between the core Go application and the IDE abstraction layer.
*   **MCP (Model Context Protocol)** - Used to expose Cercano's local inference capabilities as tools for cloud-based agents. Official Go SDK: `modelcontextprotocol/go-sdk` v1.x. (2026-03-17)

## 3. IDE Integration
*   **VS Code API** - Sidebar Chat Interface (Webview) and WorkspaceEdit API for safe code application.
*   **Zed Extension API** - Native Rust-based extension (Scaffolded).

## 4. Other Tools/Libraries
*   **Ollama** - Local LLM runtime.
*   **Qwen3-coder** - Primary local model for code generation.
*   **langchaingo** - Model-agnostic abstraction for LLM providers.
*   **Google Gemini** - Supported cloud LLM provider.
*   **Anthropic Claude** - Supported cloud LLM provider.