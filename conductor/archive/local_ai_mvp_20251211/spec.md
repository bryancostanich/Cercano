# Track Specification: Build the MVP of the Local-First AI Assistant

## 1. Job Title
Build the MVP of the local-first AI assistant, including the Go-based smart router with a gRPC interface for communication, and an initial IDE integration focused on a VS Code-compatible abstraction layer for Antigravity.

## 2. Overview
This job focuses on developing the Minimum Viable Product (MVP) of an AI development experience that intelligently leverages both local and cloud models. The core of the system will be a Go application featuring a "smart router" that directs developer requests to the most appropriate AI model. Communication with the IDE will occur via gRPC through a decoupled abstraction layer, with an initial focus on providing a VS Code-compatible integration for Antigravity.

## 3. Key Components

### 3.1 Go-based Smart Router
*   **Functionality:** An intelligent classifier that analyzes developer requests to determine their complexity and nature. It will route requests to either local or cloud-based AI models.
*   **Decision Logic:** Employs a "best guess" approach for initial routing.
*   **User Feedback:** Provides a mechanism for the user to "retry with a more powerful model" if the local model's response is insufficient.
*   **Communication:** Exposes a gRPC interface for external communication.

### 3.2 Local Model Integration
*   **Initial Capabilities:**
    *   Generating unit tests for existing functions (primary focus).
    *   Code formatting and linting.
    *   Commit message generation.
    *   Dependency analysis.
    *   Docstring and comment generation.
*   **Integration:** Designed to integrate with readily available, out-of-the-box local models capable of performing these common development tasks. No custom model training or extensive optimization for the MVP.

### 3.3 IDE Abstraction Layer (VS Code-compatible for Antigravity)
*   **Decoupling:** Completely decoupled from the core Go application, ensuring portability and future extensibility.
*   **Purpose:** Acts as an intermediary, translating IDE-specific requests into gRPC calls to the core Go application and processing responses back to the IDE.
*   **Initial Target:** Provides a VS Code-compatible integration specifically for Antigravity (Google's IDE), leveraging its presumed compatibility with the VS Code extension model.
*   **Communication:** Communicates with the core Go application via gRPC.

## 4. Requirements

### 4.1 Functional Requirements
*   **FR1:** The system shall implement a gRPC service within the Go application for receiving developer requests from the IDE abstraction layer.
*   **FR2:** The smart router shall analyze incoming requests and make an initial "best guess" decision to route them to either a local or cloud model.
*   **FR3:** The system shall support routing requests to local models for generating unit tests.
*   **FR4:** The system shall support routing requests to local models for code formatting and linting.
*   **FR5:** The system shall support routing requests to local models for commit message generation.
*   **FR6:** The system shall support routing requests to local models for dependency analysis.
*   **FR7:** The system shall support routing requests to local models for docstring and comment generation.
*   **FR8:** The system shall provide a mechanism for the user (via the IDE abstraction layer) to explicitly request a "retry with a more powerful model" if a local model's output is unsatisfactory.
*   **FR9:** The system shall provide a VS Code-compatible abstraction layer that communicates with the core Go application via gRPC.
*   **FR10:** The abstraction layer shall be designed for integration with Antigravity.

### 4.2 Non-Functional Requirements
*   **NFR1 (Performance):** Local model tasks shall exhibit low latency to maintain developer flow.
*   **NFR2 (Scalability):** The architecture shall allow for the easy addition of new local and cloud models to the smart router.
*   **NFR3 (Extensibility):** The IDE abstraction layer shall be designed to facilitate integration with other IDEs in the future.
*   **NFR4 (Usability):** The IDE integration shall feel seamless and intuitive within the developer's environment, providing clear control over model selection and retry options.
*   **NFR5 (Maintainability):** The codebase shall be modular and well-documented to allow for easy understanding and modification.