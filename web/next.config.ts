import type { NextConfig } from "next";

const nextConfig: NextConfig = {
  output: "export",
  trailingSlash: true,
  allowedDevOrigins: ["127.0.0.1"],
  // Tree-shake barrel imports for icon and UI libraries — same trick as
  // web-editor-next. `import { X } from "lucide-react"` becomes per-symbol
  // so unused icons don't get bundled. @xyflow/react is the heaviest single
  // dep on the canvas page; tree-shaking its barrel knocks the largest chunk
  // down materially.
  transpilePackages: ["@saker/editor-protocol"],
  experimental: {
    optimizePackageImports: [
      "@xyflow/react",
      "lucide-react",
      "react-icons",
      "@hugeicons/react",
      "@hugeicons/core-free-icons",
      "radix-ui",
    ],
  },
};

export default nextConfig;
