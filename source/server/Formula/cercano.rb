class Cercano < Formula
  desc "AI-powered development tool with local/cloud model routing"
  homepage "https://github.com/bryancostanich/Cercano"
  version "0.6.3"
  license "MIT"

  on_macos do
    if Hardware::CPU.arm?
      url "https://github.com/bryancostanich/Cercano/releases/download/v#{version}/cercano-darwin-arm64"
      sha256 "f3de3f0ed254aa02afa39bce278d146a487155c9b1b69372b8e8f2863d14a2bd"
    else
      url "https://github.com/bryancostanich/Cercano/releases/download/v#{version}/cercano-darwin-amd64"
      sha256 "a6d8f9603fac06289c62ea0004238cd10d312b57dd242e22bf9e243b4387ed08"
    end
  end

  on_linux do
    url "https://github.com/bryancostanich/Cercano/releases/download/v#{version}/cercano-linux-amd64"
    sha256 "ccf640bbc3de2741f43e7b98a0258872f5aed7f438f54beae70d4122b906cd4a"
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
