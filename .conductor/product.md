# Initial Concept

I want to create a new AI development experience that incorporates both cloud and local models in a way that harnesses the benefits of each to explicitly create a better workflow than today’s dominant, cloud-AI first approach. Specifically, I’d like to use one or more local models that can perform a number of common development tasks at a much faster speed than shoveling data back and forth to the cloud. And use the cloud models, when the local models are insufficient for the task at hand.

What I’d specifically like to produce, is a locally executable program that plugs into modern agentic enabled IDEs such as VS Code or Zed, and uses MCP to facilitate the integration between the IDE and the executable. The executable itself will be a Go (language) application..

From an architectural perspective, i’d like to create something that could conceptually be described as a Mixture of Experts (MoE) where the experts are models that are composable and running both locally and in the cloud. In the front of that would be some sort of router model that would be classifier and would disambiguate the user’s request and figure out the appropriate models to use to fulfill the request. One job of this classifier would be to help make requests more clear and efficient for actual models to handle. This would include asking questions to clarify important missing details, and where necessary, restate/rewrite the prompt that is handed to the underlying models.

## 2. Target Users

The primary target users are individual developers, who may also be part of larger enterprise teams. The product is designed to enhance their personal development workflow by providing a faster and more efficient AI-assisted coding experience.

## 3. Core Problems Solved

This product directly addresses two significant pain points experienced by developers using current AI coding assistants:

*   **High Latency and Slow Feedback Loops:** The constant round-trip to cloud-based AI models introduces delays that disrupt the developer's flow and slow down iterative development.
*   **High Cost:** Relying solely on powerful cloud models for every task, including trivial ones, leads to unnecessary expenses for both individual developers and enterprises.

## 4. Key Features (Minimum Viable Product - MVP)

The MVP will focus on delivering the following critical capabilities:

*   **Smart Router Model:** An intelligent classifier responsible for analyzing developer requests, determining their complexity and nature, and routing them to the most appropriate "expert" model (local or cloud). This router will also be capable of clarifying user prompts and reformulating them for optimal model performance.
*   **Local Model Integration:** The system will integrate with readily available, out-of-the-box local models capable of handling common development tasks (e.g., code completion, simple refactoring, code explanation). For the MVP, no custom model training or extensive optimization will be undertaken.
*   **IDE Integration via Local MCP Provider:** The system will integrate into IDEs (e.g., VS Code, Zed) as a local Model Context Protocol (MCP) provider. This will allow developers to add it as they would any other AI agent provider (like Gemini or Claude), enabling seamless interaction within their existing development environment.

## 5. Interaction Model

The primary interaction model will be a local MCP integration within the developer's IDE. Developers will interact with the system by adding it as an AI agent provider, similar to how they would integrate cloud-based services. This approach aims for a native and intuitive experience within the developer's familiar environment.

## Refined MVP Requirements

Here are the refined requirements based on our discussion:

### Local Model Capabilities:
*   **Primary Focus** - Generating unit tests for existing functions.
*   **Additional capabilities** - Code formatting & linting, commit message generation, dependency analysis, and docstring/comment generation.

### Smart Router Logic:
*   **Decision Criterion** - Initial "best guess" approach.
*   **User Control** - Provide a user-facing option to "retry with a more powerful model" if the local model's response is insufficient.

### IDE Integration:
*   **Target** - Prioritize "Antigravity" (Google's IDE).
*   **Implementation Strategy** - Integrate via a VS Code-compatible abstraction layer.

### Architecture:
*   **Decoupling** - The core Go application will be completely decoupled from the IDE integration layer.
*   **Communication Protocol** - gRPC will be used for communication between the core Go application and the IDE abstraction layer.
