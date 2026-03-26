class Cercano < Formula
  desc "AI-powered development tool with local/cloud model routing"
  homepage "https://github.com/bryancostanich/Cercano"
  version "0.7.0"
  license "MIT"

  on_macos do
    if Hardware::CPU.arm?
      url "https://github.com/bryancostanich/Cercano/releases/download/v#{version}/cercano-darwin-arm64"
      sha256 "b3a6a34960e14b153bfc38cab5eb307f357ea69292096358a6980ee57ed5a92b"
    else
      url "https://github.com/bryancostanich/Cercano/releases/download/v#{version}/cercano-darwin-amd64"
      sha256 "81440424b0c4a6c7982b20ba070b6d16a8213ae475f8b4698c1f67d4b8115ecd"
    end
  end

  on_linux do
    url "https://github.com/bryancostanich/Cercano/releases/download/v#{version}/cercano-linux-amd64"
    sha256 "823f84a8482109b8f60c5541c382cf4983e78e3104a1f5e21357103897a2409d"
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
