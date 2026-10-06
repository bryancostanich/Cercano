import type { Metadata } from "next";
import { IBM_Plex_Mono } from "next/font/google";
import "./globals.css";

const ibmPlexMono = IBM_Plex_Mono({
  variable: "--font-plex-mono",
  subsets: ["latin"],
  weight: ["400", "500", "600"],
  display: "swap",
});

export const metadata: Metadata = {
  metadataBase: new URL("https://cercano-ai-agent.b-c119.chatgpt.site"),
  title: "Cercano — Use context and compute deliberately",
  description: "A 100% free and open agent harness for working with frontier and open-weight models—separately or together.",
  openGraph: {
    title: "Cercano — Use context and compute deliberately",
    description: "A free and open agent harness for frontier and open-weight models, built around deliberate context and compute.",
    type: "website",
  },
  twitter: {
    card: "summary_large_image",
    title: "Cercano — Use context and compute deliberately",
    description: "A free and open agent harness for frontier and open-weight models, built around deliberate context and compute.",
  },
  alternates: { canonical: "/" },
};

const themeScript = `
(() => {
  try {
    const saved = localStorage.getItem('cercano-theme');
    const preferred = matchMedia('(prefers-color-scheme: dark)').matches ? 'night' : 'day';
    document.documentElement.dataset.theme = saved || preferred;
  } catch (_) {
    document.documentElement.dataset.theme = 'day';
  }
})();`;

export default function RootLayout({ children }: Readonly<{ children: React.ReactNode }>) {
  return (
    <html lang="en" suppressHydrationWarning>
      <head><script dangerouslySetInnerHTML={{ __html: themeScript }} /></head>
      <body className={ibmPlexMono.variable}>{children}</body>
    </html>
  );
}
