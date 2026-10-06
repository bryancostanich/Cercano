import type { NextConfig } from "next";

const isPagesBuild = process.env.CERCANO_STATIC_EXPORT === "1";
const basePath = isPagesBuild ? (process.env.NEXT_PUBLIC_BASE_PATH ?? "/Cercano") : "";

const nextConfig: NextConfig = {
  ...(isPagesBuild
    ? {
        output: "export" as const,
        basePath,
        assetPrefix: basePath,
        trailingSlash: true,
        images: { unoptimized: true },
        typescript: { tsconfigPath: "tsconfig.pages.json" },
      }
    : {}),
};

export default nextConfig;
