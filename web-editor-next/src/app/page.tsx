"use client";

import { Suspense, useEffect } from "react";
import { useRouter, useSearchParams } from "next/navigation";

// Server-side redirect() drops the query string, which would lose any pending
// ?import=... bridge payload. Do a client-side replace instead so the import
// payload survives the bounce from /editor/?import=... → /editor/editor/?import=...
export default function Home() {
	return (
		<Suspense fallback={null}>
			<RedirectToEditor />
		</Suspense>
	);
}

function RedirectToEditor() {
	const router = useRouter();
	const searchParams = useSearchParams();

	useEffect(() => {
		const qs = searchParams.toString();
		router.replace(qs ? `/editor/?${qs}` : "/editor/");
	}, [router, searchParams]);

	return null;
}
