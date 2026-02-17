fn main() -> Result<(), Box<dyn std::error::Error>> {
    tonic_build::configure()
        .build_server(false) // We only need the client in the extension
        .compile(
            &["../source/proto/agent.proto"],
            &["../source/proto"], // Proto include path
        )?;
    Ok(())
}
