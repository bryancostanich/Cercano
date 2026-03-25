class Cercano < Formula
  desc "AI-powered development tool with local/cloud model routing"
  homepage "https://github.com/bryancostanich/Cercano"
  version "0.6.2"
  license "MIT"

  on_macos do
    if Hardware::CPU.arm?
      url "https://github.com/bryancostanich/Cercano/releases/download/v#{version}/cercano-darwin-arm64"
      sha256 "3f58cb99ae5c93ecde853a19ba5cb44fe1bd616c1e1a15373af7a046c504a6ac"
    else
      url "https://github.com/bryancostanich/Cercano/releases/download/v#{version}/cercano-darwin-amd64"
      sha256 "9012e590681c3ace3f3e9b97cba1db2e0e308f7631bcc5c58cfaba9a6f32f3a3"
    end
  end

  on_linux do
    url "https://github.com/bryancostanich/Cercano/releases/download/v#{version}/cercano-linux-amd64"
    sha256 "a23a66c267d75bf7f92b15829a9cf4fb175ed32f310b03b1bdfbeb68e8157aad"
  end

  def install
    binary = Dir.glob("cercano-*").first || "cercano"
    bin.install binary => "cercano"
  end

  def caveats
    <<~EOS
      Cercano requires Ollama for local AI inference.
      Install it from https://ollama.com/ then run:

        cercano setup

      To use with Claude Code:

        claude mcp add cercano -- cercano --mcp
    EOS
  end

  test do
    assert_match "cercano v", shell_output("#{bin}/cercano --version")
  end
end
