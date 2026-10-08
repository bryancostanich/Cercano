"use client";

import { useEffect, useState } from "react";

const projectUrl = "https://github.com/bryancostanich/Cercano";
const basePath = process.env.NEXT_PUBLIC_BASE_PATH ?? "";
const assetUrl = (path: string) => `${basePath}${path}`;

const features = [
  {
    code: "01 / ROUTING",
    title: "The right model for the job.",
    lead: "Context and compute are choices, not defaults.",
    body: "Cercano routes work by capability, quality, and destination. Keep demanding reasoning with a frontier model, move bounded work to an efficient model, or run it locally.",
    doc: "advanced-routing.md",
  },
  {
    code: "02 / DELEGATION",
    title: "Delegate work. Keep your focus.",
    lead: "Subagents work in parallel without crowding the main conversation.",
    body: "Dispatch research, implementation, and verification into separate, scoped contexts. Open any subagent in its own tab, follow its progress, and bring back only what matters.",
    doc: "built-in-delegation.md",
    video: {
      src: "/videos/delegation.mp4",
      poster: "/videos/delegation.jpg",
      label: "Delegation and subagent tabs in Cercano",
      caption: "Dispatch bounded work, then inspect each agent in its own terminal tab.",
    },
  },
  {
    code: "03 / WORKFLOWS",
    title: "Process, not just a prompt.",
    lead: "Good agent work is a sequence of deliberate moves.",
    body: "Research, planning, subagent execution, systematic debugging, and design decisions are built into the harness. The process stays visible and repeatable instead of living in one oversized instruction.",
    doc: "powerful-agent-workflows.md",
  },
  {
    code: "04 / CONTEXT",
    title: "Catch up without starting over.",
    lead: "Long sessions keep moving while context is managed in the background.",
    body: "Layered summaries and non-blocking compaction preserve decisions, current state, and useful history. You can leave, return, and understand where the work stands.",
    doc: "context-management.md",
  },
  {
    code: "05 / METRICS",
    title: "Know where the tokens go.",
    lead: "Usage should be legible enough to change a decision.",
    body: "See token use by provider, model, source, and time. Cercano makes the tradeoffs between local, efficient, and frontier inference concrete.",
    doc: "advanced-metrics.md",
    video: {
      src: "/videos/metrics.mp4",
      poster: "/videos/metrics.jpg",
      label: "Token metrics in Cercano",
      caption: "Inspect model and provider usage without leaving the terminal.",
    },
  },
  {
    code: "06 / LOCAL",
    title: "Put local models to work.",
    lead: "Open-weight models belong inside the workflow, not beside it.",
    body: "Cercano manages a local runtime and makes local inference a first-class destination for delegated work, private tasks, and zero-cost iteration.",
    doc: "integrated-local-runtime.md",
  },
  {
    code: "07 / PROVIDERS",
    title: "Choose your frontier provider.",
    lead: "Use subscription sign-in, API credentials, or compatible endpoints.",
    body: "Provider choice remains separate from the coding workflow around it. Switch the underlying model or endpoint without rebuilding how you work.",
    doc: "openai-providers.md",
  },
  {
    code: "08 / TERMINAL",
    title: "A terminal that stays readable.",
    lead: "The interface is dense where it helps and quiet everywhere else.",
    body: "Responsive layouts, rich formatting, visible tool activity, context meters, subagent tabs, and carefully tuned themes make long sessions comfortable.",
    doc: "advanced-terminal-ui.md",
    video: {
      src: "/videos/terminal-ui.mp4",
      poster: "/videos/terminal-ui.jpg",
      label: "Cercano terminal interface and themes",
      caption: "A polished terminal interface, including the palettes behind this site.",
    },
  },
  {
    code: "09 / SESSIONS",
    title: "Resume the session, not the setup.",
    lead: "History is useful when it is searchable and ready to continue.",
    body: "Automatic titles, saved sessions, search, and resume keep ongoing work close. Pick up the thread without reconstructing the task from memory.",
    doc: "automatic-session-retention.md",
    video: {
      src: "/videos/sessions.mp4",
      poster: "/videos/sessions.jpg",
      label: "Searching and resuming a Cercano session",
      caption: "Find a previous session and continue from the terminal.",
    },
  },
  {
    code: "10 / ARCHITECTURE",
    title: "One agent. More than one client.",
    lead: "The interface and the durable agent core are separate.",
    body: "An independent service owns sessions, tools, and model access. The terminal stays fast and focused, while headless clients and future interfaces can use the same agent.",
    doc: "client-server-architecture.md",
    architecture: true,
  },
  {
    code: "11 / OPEN SOURCE",
    title: "Free to use. Open to inspect.",
    lead: "Cercano is 100% free and open source under Apache 2.0.",
    body: "Run it, study it, extend it, and make it your own. The complete agent, terminal, workflows, and local-runtime integration live in the public repository.",
    href: `${projectUrl}/blob/main/LICENSE`,
    linkLabel: "Read the license",
  },
];

export default function Home() {
  const [theme, setTheme] = useState<"day" | "night">("day");
  const [copied, setCopied] = useState(false);

  useEffect(() => {
    const active = document.documentElement.dataset.theme === "night" ? "night" : "day";
    setTheme(active);
  }, []);

  const toggleTheme = () => {
    const next = theme === "night" ? "day" : "night";
    document.documentElement.dataset.theme = next;
    window.localStorage.setItem("cercano-theme", next);
    setTheme(next);
  };

  const copyInstallCommand = async () => {
    await navigator.clipboard.writeText("brew install bryancostanich/tap/cercano");
    setCopied(true);
    window.setTimeout(() => setCopied(false), 1800);
  };

  return (
    <div className="page-shell">
      <header className="site-header">
        <a className="brand" href="#top" aria-label="Cercano home">
          <span className="prompt" aria-hidden="true">&gt;_</span>
          <span>cercano</span>
        </a>
        <nav className="primary-nav" aria-label="Primary navigation">
          <a href="#features">Features</a>
          <a href="#install">Install</a>
          <a href={`${projectUrl}/tree/main/docs/agent/features`}>Docs</a>
          <a href={projectUrl}>GitHub ↗</a>
          <button
            className="theme-toggle"
            type="button"
            onClick={toggleTheme}
            aria-label={`Switch to ${theme === "night" ? "daylight" : "night"} theme`}
            aria-pressed={theme === "night"}
          >
            <span>{theme === "night" ? "Night" : "Day"}</span>
            <span className="theme-track" aria-hidden="true"><span /></span>
          </button>
        </nav>
      </header>

      <main id="top">
        <section className="hero" aria-labelledby="hero-title">
          <div>
            <p className="eyebrow">100% free + open · terminal native</p>
            <h1 id="hero-title">Use context and compute deliberately.</h1>
          </div>
          <div className="hero-aside">
            <p className="lede">Cercano is a premium agent harness for working with frontier and open-weight models—separately or together.</p>
            <a className="command-link" href="#install">
              <code>brew install bryancostanich/tap/cercano</code>
              <span>Install now</span>
            </a>
          </div>
        </section>

        <section className="manifesto" aria-label="Cercano approach">
          <p className="section-label">Frontier + open</p>
          <p className="manifesto-copy">A polished terminal, built-in workflows, and deliberate routing put <em>every kind of model</em> inside one coherent way of working.</p>
        </section>

        <section className="features" id="features" aria-labelledby="features-title">
          <div className="features-heading">
            <p className="section-label">What Cercano does</p>
            <h2 id="features-title">An agent designed around the work.</h2>
          </div>

          <div className="feature-list">
            {features.map((feature, index) => (
              <article className={`feature-story${feature.video || feature.architecture ? " feature-story--visual" : ""}`} key={feature.code}>
                <div className="feature-copy">
                  <span className="feature-code">{feature.code}</span>
                  <h3>{feature.title}</h3>
                  <p className="feature-lead">{feature.lead}</p>
                  <p className="feature-body">{feature.body}</p>
                  <a className="text-link" href={feature.href ?? `${projectUrl}/blob/main/docs/agent/features/${feature.doc}`}>
                    {feature.linkLabel ?? "Read the feature guide"} <span aria-hidden="true">↗</span>
                  </a>
                </div>

                {feature.video && (
                  <figure className="feature-demo">
                    <div className="video-frame">
                      <video controls playsInline preload="metadata" poster={assetUrl(feature.video.poster)} aria-label={feature.video.label}>
                        <source src={assetUrl(feature.video.src)} type="video/mp4" />
                        Your browser does not support embedded video.
                      </video>
                    </div>
                    <figcaption><span>{String(index + 1).padStart(2, "0")}</span>{feature.video.caption}</figcaption>
                  </figure>
                )}

                {feature.architecture && (
                  <div className="flow" aria-label="Terminal and headless clients connect to the agent service, which routes work to local and hosted models">
                    <div className="flow-column"><span>Clients</span><strong>Terminal UI</strong><strong>Headless</strong></div>
                    <span className="flow-arrow" aria-hidden="true">→</span>
                    <div className="flow-column flow-core"><span>Core</span><strong>Agent service</strong><small>sessions · tools · state</small></div>
                    <span className="flow-arrow" aria-hidden="true">→</span>
                    <div className="flow-column"><span>Models</span><strong>Frontier</strong><strong>Open weight</strong></div>
                  </div>
                )}
              </article>
            ))}
          </div>
        </section>

        <section className="install" id="install" aria-labelledby="install-title">
          <div className="install-copy">
            <p className="section-label">Install Cercano</p>
            <h2 id="install-title">Install in one line.</h2>
            <p>Homebrew installation is available now for macOS. Package-manager releases for Linux and Windows are next.</p>
          </div>

          <div className="install-panel" aria-label="Cercano installation options">
            <div className="installer-heading">
              <span>Homebrew</span>
              <span className="availability">Available now</span>
            </div>
            <div className="install-command">
              <code><span>$</span> brew install bryancostanich/tap/cercano</code>
              <button type="button" onClick={copyInstallCommand} aria-label="Copy Homebrew install command">
                {copied ? "Copied" : "Copy"}
              </button>
            </div>
            <p className="after-install">Then run <code>cercano</code>. Configure providers, local models, routing, and permissions from inside the TUI.</p>

            <div className="package-roadmap" aria-label="Upcoming package managers">
              <div><span>apt</span><small>Linux · coming soon</small></div>
              <div><span>Chocolatey</span><small>Windows · coming soon</small></div>
            </div>

            <div className="install-links">
              <a href={`${projectUrl}/releases`}>Releases</a>
              <a href={`${projectUrl}/blob/main/docs/agent/self-dev.md`}>Build from source</a>
            </div>
          </div>
        </section>

        <section className="closing" aria-label="Project documentation">
          <div>
            <p className="section-label">Go deeper</p>
            <h2>Context is a resource.<br />Spend it well.</h2>
          </div>
          <nav className="project-links" aria-label="Project links">
            <a href={projectUrl}><span>GitHub repository</span><span>↗</span></a>
            <a href={`${projectUrl}/tree/main/docs/agent`}><span>Agent guide</span><span>↗</span></a>
            <a href={`${projectUrl}/blob/main/docs/cloud-routing.md`}><span>Routing guide</span><span>↗</span></a>
            <a href={`${projectUrl}/tree/main/docs/agent/features`}><span>Feature guide</span><span>↗</span></a>
          </nav>
        </section>
      </main>

      <footer><a href={projectUrl}>Cercano - 100% Free + Open</a></footer>
    </div>
  );
}
