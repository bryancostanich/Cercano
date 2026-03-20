class Cercano < Formula
  desc "AI-powered development tool with local/cloud model routing"
  homepage "https://github.com/bryancostanich/Cercano"
  # TODO: Switch to GitHub Release URL when repo is public:
  #   url "https://github.com/bryancostanich/Cercano/archive/refs/tags/v#{version}.tar.gz"
  url "file:///tmp/cercano-head.tar.gz"
  sha256 "7dec6bde85a9acab31ff18cdb592136e7438f65dbac4e28d3fd0b3077b00d457"
  version "0.5.0-rc1"
  license "MIT"

  depends_on "go" => :build

  def install
    cd "source/server" do
      ldflags = "-X main.version=#{version}"
      system "go", "build", "-ldflags", ldflags, "-o", bin/"cercano", "./cmd/cercano"
    end
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
