import { t } from "@lingui/core/macro"
import { Trans } from "@lingui/react/macro"
import { DownloadIcon, LoaderCircleIcon, RefreshCwIcon } from "lucide-react"
import { useCallback, useEffect, useMemo, useState } from "react"
import { Button } from "@/components/ui/button"
import { Card, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { Checkbox } from "@/components/ui/checkbox"
import { Input } from "@/components/ui/input"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { toast } from "@/components/ui/use-toast"
import { pb } from "@/lib/api"
import { cn } from "@/lib/utils"
import type { PackageUpdate } from "@/types"

export default function PackageUpdatesTable({ systemId }: { systemId: string }) {
	const [updates, setUpdates] = useState<PackageUpdate[] | null>(null)
	const [selected, setSelected] = useState<Set<string>>(new Set())
	const [checking, setChecking] = useState(false)
	const [applying, setApplying] = useState(false)
	const [unsupported, setUnsupported] = useState(false)
	const [filter, setFilter] = useState("")

	const fetchUpdates = useCallback(
		async (refresh: boolean) => {
			const { updates } = await pb.send<{ updates: PackageUpdate[] }>(
				`/api/beszel-ext/systems/${systemId}/updates/check`,
				{
					method: "POST",
					body: { refresh },
				}
			)
			setUpdates(updates ?? [])
			setSelected((current) => {
				const names = new Set((updates ?? []).map((u) => u.n))
				return new Set([...current].filter((name) => names.has(name)))
			})
		},
		[systemId]
	)

	// initial load returns the agent's cached list (fast)
	useEffect(() => {
		setUpdates(null)
		setSelected(new Set())
		setUnsupported(false)
		fetchUpdates(false).catch(() => {
			// agent doesn't support package updates (old version / no package manager)
			setUnsupported(true)
		})
	}, [fetchUpdates])

	const checkNow = useCallback(async () => {
		setChecking(true)
		try {
			await fetchUpdates(true)
		} catch (error: any) {
			toast({
				title: t`Error`,
				description: error?.message ?? t`Failed to check for updates`,
			})
		} finally {
			setChecking(false)
		}
	}, [fetchUpdates])

	const applySelected = useCallback(async () => {
		const packages = [...selected]
		if (!packages.length) {
			return
		}
		setApplying(true)
		try {
			const { results } = await pb.send<{ results: Record<string, string> }>(
				`/api/beszel-ext/systems/${systemId}/updates/apply`,
				{
					method: "POST",
					body: { packages },
					// applying updates can take a while
					requestKey: null,
				}
			)
			const failed = Object.entries(results ?? {}).filter(([, msg]) => msg !== "")
			const okCount = packages.length - failed.length
			if (failed.length) {
				toast({
					title: t`Some packages failed to update`,
					description: `${t`Updated`}: ${okCount}. ${t`Failed`}: ${failed
						.map(([name, msg]) => `${name} (${msg})`)
						.join(", ")}`,
				})
			} else {
				toast({
					title: t`Packages updated`,
					description: `${t`Updated`}: ${packages.join(", ")}`,
				})
			}
			setSelected(new Set())
			// agent re-checks after applying, so its cached list is already fresh
			await fetchUpdates(false)
		} catch (error: any) {
			toast({
				title: t`Error`,
				description: error?.message ?? t`Failed to apply updates`,
			})
		} finally {
			setApplying(false)
		}
	}, [systemId, selected, fetchUpdates])

	const filteredUpdates = useMemo(() => {
		if (!updates) {
			return []
		}
		const terms = filter.toLowerCase().split(" ").filter(Boolean)
		if (!terms.length) {
			return updates
		}
		return updates.filter((u) => terms.every((term) => u.n.toLowerCase().includes(term)))
	}, [updates, filter])

	const allFilteredSelected = filteredUpdates.length > 0 && filteredUpdates.every((u) => selected.has(u.n))

	const toggleAll = useCallback(() => {
		setSelected((current) => {
			const next = new Set(current)
			if (filteredUpdates.every((u) => next.has(u.n))) {
				for (const u of filteredUpdates) {
					next.delete(u.n)
				}
			} else {
				for (const u of filteredUpdates) {
					next.add(u.n)
				}
			}
			return next
		})
	}, [filteredUpdates])

	const toggleOne = useCallback((name: string) => {
		setSelected((current) => {
			const next = new Set(current)
			if (next.has(name)) {
				next.delete(name)
			} else {
				next.add(name)
			}
			return next
		})
	}, [])

	// hide entirely if the agent doesn't support package updates or hasn't responded yet
	if (unsupported || updates === null) {
		return null
	}

	return (
		<Card className="p-6 @container w-full">
			<CardHeader className="p-0 mb-4">
				<div className="grid md:flex gap-5 w-full items-end">
					<div className="px-2 sm:px-1">
						<CardTitle className="mb-2">
							<Trans>Package Updates</Trans>
						</CardTitle>
						<CardDescription>
							{updates.length ? (
								<Trans>
									Available: {updates.length}. Nothing is installed without your explicit approval.
								</Trans>
							) : (
								<Trans>All packages are up to date.</Trans>
							)}
						</CardDescription>
					</div>
					<div className="flex gap-2 ms-auto items-center flex-wrap">
						{updates.length > 0 && (
							<Input
								placeholder={t`Filter...`}
								value={filter}
								onChange={(e) => setFilter(e.target.value)}
								className="px-4 w-full max-w-full md:w-52"
							/>
						)}
						<Button variant="outline" size="sm" onClick={checkNow} disabled={checking || applying}>
							{checking ? (
								<LoaderCircleIcon className="size-4 me-1.5 animate-spin" />
							) : (
								<RefreshCwIcon className="size-4 me-1.5" />
							)}
							<Trans>Check now</Trans>
						</Button>
						{updates.length > 0 && (
							<Button size="sm" onClick={applySelected} disabled={applying || checking || selected.size === 0}>
								{applying ? (
									<LoaderCircleIcon className="size-4 me-1.5 animate-spin" />
								) : (
									<DownloadIcon className="size-4 me-1.5" />
								)}
								<Trans>Apply selected</Trans> ({selected.size})
							</Button>
						)}
					</div>
				</div>
			</CardHeader>
			{updates.length > 0 && (
				<div className={cn("max-h-[calc(100dvh-17rem)] relative overflow-auto border rounded-md", applying && "opacity-60 pointer-events-none")}>
					<Table className="text-sm w-full text-nowrap">
						<TableHeader className="sticky top-0 z-10 bg-card">
							<TableRow>
								<TableHead className="w-10 px-3">
									<Checkbox
										checked={allFilteredSelected}
										onCheckedChange={toggleAll}
										aria-label={t`Select all`}
									/>
								</TableHead>
								<TableHead>
									<Trans>Package</Trans>
								</TableHead>
								<TableHead>
									<Trans>Current version</Trans>
								</TableHead>
								<TableHead>
									<Trans>Available version</Trans>
								</TableHead>
							</TableRow>
						</TableHeader>
						<TableBody>
							{filteredUpdates.length ? (
								filteredUpdates.map((update) => (
									<TableRow
										key={update.n}
										className="cursor-pointer"
										data-state={selected.has(update.n) ? "selected" : undefined}
										onClick={() => toggleOne(update.n)}
									>
										<TableCell className="px-3 py-2.5">
											<Checkbox
												checked={selected.has(update.n)}
												onCheckedChange={() => toggleOne(update.n)}
												aria-label={update.n}
												onClick={(e) => e.stopPropagation()}
											/>
										</TableCell>
										<TableCell className="py-2.5 font-medium">{update.n}</TableCell>
										<TableCell className="py-2.5 text-muted-foreground">{update.cv || "—"}</TableCell>
										<TableCell className="py-2.5">{update.av || "—"}</TableCell>
									</TableRow>
								))
							) : (
								<TableRow>
									<TableCell colSpan={4} className="h-24 text-center pointer-events-none">
										<Trans>No results.</Trans>
									</TableCell>
								</TableRow>
							)}
						</TableBody>
					</Table>
				</div>
			)}
		</Card>
	)
}
