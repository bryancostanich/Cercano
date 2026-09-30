"use client";

import { useEffect, useState } from "react";

const capabilities = [
  {
    code: "01 / CONTEXT",
    title: "Context that keeps moving",
    body: "Rolling background compaction and layered summaries keep long conversations useful without pausing the work.",
  },
  {
    code: "02 / DELEGATION",
    title: "Bounded work, separate context",
    body: "Subagents explore and execute with scoped tools, keeping routine work outside the main reasoning thread.",
  },
  {
    code: "03 / ROUTING",
    title: "Capability, quality, destination",
    body: "Choose task class, model tier, and where it runs independently—instead of sending every job down one route.",
  },
  {
    code: "04 / LOCAL",
    title: "Open weights, managed",
    body: "A managed local runtime puts your hardware to work without assembling a separate inference stack.",
  },
  {
    code: "05 / METRICS",
    title: "See where tokens go",
    body: "Usage broken down by provider, model, and source makes routing and budget decisions visible.",
  },
  {
    code: "06 / TERMINAL",
    title: "A TUI built for real work",
    body: "Responsive layouts, rich formatting, themes, a context meter, and visible tool and subagent activity.",
  },
  {
    code: "07 / SESSIONS",
    title: "Leave. Return. Continue.",
    body: "Automatic titles, saved history, search, and resume preserve the work without reconstructing it.",
  },
  {
    code: "08 / PROVIDERS",
    title: "Bring your endpoints",
    body: "Connect compatible hosted and self-hosted providers without changing the coding workflow around them.",
  },
  {
    code: "09 / ARCHITECTURE",
    title: "Interface and agent, separated",
    body: "An independent agent process serves terminal and headless clients while keeping their sessions distinct.",
  },
];

const projectUrl = "https://github.com/bryancostanich/Cercano";

export default function Home() {
  const [theme, setTheme] = useState<"day" | "night">("day");

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

  return (
    <div className="page-shell">
      <header className="site-header">
        <a className="brand" href="#top" aria-label="Cercano home">
          <span className="prompt" aria-hidden="true">&gt;_</span>
          <span>cercano</span>
        </a>
        <nav className="primary-nav" aria-label="Primary navigation">
          <a href="#capabilities">Capabilities</a>
          <a href="#start">Get started</a>
          <a href={`${projectUrl}/tree/main/docs/agent`}>Docs</a>
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
            <p className="eyebrow">AI coding agent · terminal native</p>
            <h1 id="hero-title">Use context and compute deliberately.</h1>
          </div>
          <div className="hero-aside">
            <p className="lede">Cercano brings frontier reasoning, built-in delegation, and open-weight models into one terminal workflow.</p>
            <a className="command-link" href="#start">
              <code>cercano</code>
              <span>Get started ↓</span>
            </a>
          </div>
        </section>

        <section className="manifesto" aria-label="Cercano approach">
          <p className="section-label">One workflow, many routes</p>
          <p className="manifesto-copy">Keep demanding reasoning in the main conversation. <em>Delegate bounded work.</em> Decide what runs locally and what earns frontier compute.</p>
        </section>

        <section className="capabilities" id="capabilities" aria-labelledby="capabilities-label">
          <h2 className="visually-hidden" id="capabilities-label">What makes Cercano different</h2>
          <div className="capability-grid">
            {capabilities.map((capability) => (
              <article className="capability" key={capability.code}>
                <span className="capability-code">{capability.code}</span>
                <h3>{capability.title}</h3>
                <p>{capability.body}</p>
              </article>
            ))}
          </div>
        </section>

        <section className="architecture" aria-labelledby="architecture-title">
          <div>
            <p className="section-label">Client / server architecture</p>
            <h2 id="architecture-title">A thin client around a <span>durable agent core.</span></h2>
          </div>
          <div className="flow" aria-label="Terminal client connects to the agent service, which routes work to local and hosted models">
            <div className="flow-item">Terminal UI<small>interactive</small></div>
            <span className="flow-arrow" aria-hidden="true">→</span>
            <div className="flow-item">Agent service<small>state + tools</small></div>
            <span className="flow-arrow" aria-hidden="true">→</span>
            <div className="flow-item">Models<small>local / hosted</small></div>
          </div>
        </section>

        <section className="start" id="start" aria-labelledby="start-title">
          <div className="start-copy">
            <p className="section-label">Get started</p>
            <h2 id="start-title">One command.<br />Deliberate by default.</h2>
            <p>Build the server and terminal client, then configure models, routing, and permissions from inside the TUI. Start with a bounded, read-only task in a repository you know.</p>
          </div>
          <div className="commands" aria-label="Cercano commands">
            <div className="command-row"><code><span>$</span> cercano</code><small>Interactive terminal</small></div>
            <div className="command-row"><code><span>$</span> cercano run &quot;Explain this repository&quot;</code><small>Scripts and automation</small></div>
            <p>Standalone releases are in progress. For now, follow the source build and agent setup guides.</p>
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

      <footer>
        <span>Cercano - 100% Free + Open</span>
      </footer>
    </div>
  );
}
