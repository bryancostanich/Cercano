# Project Technology Stack

This document outlines the core technologies used in the project.

## 1. Programming Languages
*   **Go (Golang)** - The primary programming language for the core application, including the smart router and local model integration.
*   **TypeScript** - Used for the VS Code Extension.
*   **Rust** - Used for the Zed Extension.

## 2. Communication Frameworks
*   **gRPC** - Used for high-performance, contract-first communication between the core Go application and the IDE abstraction layer.

## 3. IDE Integration
*   **VS Code API** - Sidebar Chat Interface (Webview).
*   **Zed Extension API** - Native Rust-based extension (Scaffolded).

## 4. Other Tools/Libraries
*   **Ollama** - Local LLM runtime.
*   **Qwen3-coder** - Primary local model for code generation.