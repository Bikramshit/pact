import type { NextConfig } from "next";

const dev = process.env.NODE_ENV === "development";

// Production is a static export that pact-core serves from the control-plane
// port, so the dashboard has no server of its own. In development, `next dev`
// proxies API calls to a pact-core running on :8080.
const config: NextConfig = dev
  ? {
      trailingSlash: true,
      skipTrailingSlashRedirect: true,
      agentRules: false,
      compress: false, // keeps the proxied event stream from being buffered
      async rewrites() {
        return [{ source: "/v1/:path*", destination: "http://127.0.0.1:8080/v1/:path*" }];
      },
    }
  : {
      output: "export",
      trailingSlash: true,
      images: { unoptimized: true },
      agentRules: false,
    };

export default config;
