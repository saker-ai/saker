import type { NextConfig } from "next";

const nextConfig: NextConfig = {
	output: "export",
	basePath: "/editor",
	trailingSlash: true,
	reactStrictMode: true,
	images: { unoptimized: true },
	// Tree-shake barrel imports for icon and UI libraries — turns
	// `import { X } from "radix-ui"` into a per-symbol import so unused
	// primitives don't get bundled. Significant size win for radix-ui
	// (which re-exports every primitive from one barrel) and the icon
	// libraries we touch from many files.
	experimental: {
		optimizePackageImports: [
			"radix-ui",
			"lucide-react",
			"react-icons",
			"@hugeicons/react",
			"@hugeicons/core-free-icons",
		],
	},
};

export default nextConfig;
