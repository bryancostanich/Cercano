use zed_extension_api as zed;

pub mod agent {
    tonic::include_proto!("agent");
}

use agent::agent_client::AgentClient;
use agent::ProcessRequestRequest;

struct CercanoExtension;

impl zed::Extension for CercanoExtension {
    fn new() -> Self {
        Self
    }
}

zed::register_extension!(CercanoExtension);