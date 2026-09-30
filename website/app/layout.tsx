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
  description: "An AI coding agent that combines frontier reasoning, built-in delegation, and open-weight models in one terminal workflow.",
  openGraph: {
    title: "Cercano — Use context and compute deliberately",
    description: "Frontier reasoning, built-in delegation, and open-weight models in one terminal workflow.",
    type: "website",
  },
  twitter: {
    card: "summary_large_image",
    title: "Cercano — Use context and compute deliberately",
    description: "Frontier reasoning, built-in delegation, and open-weight models in one terminal workflow.",
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
