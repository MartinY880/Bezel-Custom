import { t } from "@lingui/core/macro"
import { Trans } from "@lingui/react/macro"
import { DownloadIcon, LoaderCircleIcon, RefreshCwIcon, ShieldAlertIcon } from "lucide-react"
import { useCallback, useEffect, useMemo, useRef, useState } from "react"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Card, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { Checkbox } from "@/components/ui/checkbox"
import { Input } from "@/components/ui/input"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { toast } from "@/components/ui/use-toast"
import { pb } from "@/lib/api"
import { cn } from "@/lib/utils"
import type { PackageUpdate } from "@/types"

interface ApplyStatus {
	status: "idle" | "running" | "done" | "failed" | "unreachable"
	packages?: string[]
	message?: string
	startedAt?: number
	finishedAt?: number
}

const POLL_INTERVAL = 5_000

export default function PackageUpdatesTable({ systemId }: { systemId: string }) {
	const [updates, setUpdates] = useState<PackageUpdate[] | null>(null)
	const [selected, setSelected] = useState<Set<string>>(new Set())
	const [checking, setChecking] = useState(false)
	const [applying, setApplying] = useState<ApplyStatus | null>(null)
	const [unsupported, setUnsupported] = useState(false)
	const [filter, setFilter] = useState("")
	// job completions already announced, so re-polls don't re-toast
	const announcedJob = useRef<number>(0)

	const fetchUpdates = useCallback(
		async (refresh: boolean) => {
			const { updates } = await pb.send<{ updates: PackageUpdate[] }>(
				`/api/beszel-ext/systems/${systemId}/updates/check`,
				{
					method: "POST",
					body: { refresh },
					requestKey: null,
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

	// initial load: cached list + resume watching any in-flight apply
	useEffect(() => {
		setUpdates(null)
		setSelected(new Set())
		setUnsupported(false)
		setApplying(null)
		announcedJob.current = 0

		fetchUpdates(false).catch(() => {
			// agent doesn't support package updates (old version / no package manager)
			setUnsupported(true)
		})

		pb.send<ApplyStatus>(`/api/beszel-ext/systems/${systemId}/updates/status`, {
			method: "GET",
			requestKey: null,
		})
			.then((status) => {
				if (status.status === "running") {
					// an apply started earlier is still going — resume showing it
					announcedJob.current = status.startedAt ?? 0
					setApplying(status)
				}
			})
			.catch(() => {})
	}, [systemId, fetchUpdates])

	// poll job status while an apply is running
	useEffect(() => {
		if (applying?.status !== "running" && applying?.status !== "unreachable") {
			return
		}
		const id = setInterval(async () => {
			try {
				const status = await pb.send<ApplyStatus>(`/api/beszel-ext/systems/${systemId}/updates/status`, {
					method: "GET",
					requestKey: null,
				})
				if (status.status === "running") {
					setApplying(status)
					return
				}
				if (status.status === "unreachable") {
					// agent briefly offline mid-upgrade (service restarts) — keep waiting
					setApplying((cur) => ({ ...(cur ?? status), status: "unreachable" }))
					return
				}
				// job finished (done/failed) or agent restarted with no job (idle)
				setApplying(null)
				const jobKey = status.finishedAt ?? Date.now()
				if (announcedJob.current !== jobKey) {
					announcedJob.current = jobKey
					if (status.status === "done") {
						toast({
							title: t`Packages updated`,
							description: `${status.packages?.length ?? ""} ${t`packages installed successfully`}`,
						})
					} else if (status.status === "failed") {
						toast({
							title: t`Package update failed`,
							description: status.message ?? t`Check the agent logs for details`,
						})
					}
				}
				setSelected(new Set())
				fetchUpdates(false).catch(() => {})
			} catch {
				// hub unreachable — keep the polling loop alive
			}
		}, POLL_INTERVAL)
		return () => clearInterval(id)
	}, [applying?.status, systemId, fetchUpdates])

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
		try {
			const status = await pb.send<ApplyStatus>(`/api/beszel-ext/systems/${systemId}/updates/apply`, {
				method: "POST",
				body: { packages },
				requestKey: null,
			})
			// legacy (pre-async) agents apply synchronously and return a final
			// result right away — report it instead of polling
			if (status.status === "done" || status.status === "failed") {
				announcedJob.current = status.finishedAt ?? Date.now()
				if (status.status === "done") {
					toast({ title: t`Packages updated`, description: `${packages.length} ${t`packages installed successfully`}` })
				} else {
					toast({ title: t`Package update failed`, description: status.message ?? t`Check the agent logs for details` })
				}
				setSelected(new Set())
				fetchUpdates(false).catch(() => {})
				return
			}
			announcedJob.current = 0
			setApplying(status.status ? status : { status: "running", packages })
		} catch (error: any) {
			toast({
				title: t`Error`,
				description: error?.message ?? t`Failed to start update`,
			})
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

	// held packages (apt-mark hold) can't be applied; they're never selectable
	const selectableUpdates = useMemo(() => filteredUpdates.filter((u) => !u.hd), [filteredUpdates])
	const securityCount = useMemo(() => (updates ?? []).filter((u) => u.sec && !u.hd).length, [updates])

	const selectSecurity = useCallback(() => {
		setSelected(new Set((updates ?? []).filter((u) => u.sec && !u.hd).map((u) => u.n)))
	}, [updates])

	const allFilteredSelected = selectableUpdates.length > 0 && selectableUpdates.every((u) => selected.has(u.n))

	const toggleAll = useCallback(() => {
		setSelected((current) => {
			const next = new Set(current)
			if (selectableUpdates.every((u) => next.has(u.n))) {
				for (const u of selectableUpdates) {
					next.delete(u.n)
				}
			} else {
				for (const u of selectableUpdates) {
					next.add(u.n)
				}
			}
			return next
		})
	}, [selectableUpdates])

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

	const isApplying = applying?.status === "running" || applying?.status === "unreachable"

	return (
		<Card className="p-6 @container w-full">
			<CardHeader className="p-0 mb-4">
				<div className="grid md:flex gap-5 w-full items-end">
					<div className="px-2 sm:px-1">
						<CardTitle className="mb-2">
							<Trans>Package Updates</Trans>
						</CardTitle>
						<CardDescription>
							{isApplying ? (
								<span className="flex items-center gap-1.5">
									<LoaderCircleIcon className="size-3.5 animate-spin" />
									{applying?.status === "unreachable" ? (
										<Trans>Applying updates — agent is restarting, waiting for it to come back…</Trans>
									) : (
										<Trans>
											Applying {applying?.packages?.length ?? 0} updates in the background — safe to leave this page.
										</Trans>
									)}
								</span>
						) : updates.length ? (
								<>
									<Trans>
										Available: {updates.length}. Nothing is installed without your explicit approval.
									</Trans>
									{securityCount > 0 && (
										<span className="text-red-500 ms-1.5">
											<Trans>Security: {securityCount}.</Trans>
										</span>
									)}
								</>
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
						{securityCount > 0 && (
							<Button variant="outline" size="sm" onClick={selectSecurity} disabled={isApplying || checking}>
								<ShieldAlertIcon className="size-4 me-1.5 text-red-500" />
								<Trans>Select security</Trans> ({securityCount})
							</Button>
						)}
						<Button variant="outline" size="sm" onClick={checkNow} disabled={checking || isApplying}>
							{checking ? (
								<LoaderCircleIcon className="size-4 me-1.5 animate-spin" />
							) : (
								<RefreshCwIcon className="size-4 me-1.5" />
							)}
							<Trans>Check now</Trans>
						</Button>
						{updates.length > 0 && (
							<Button size="sm" onClick={applySelected} disabled={isApplying || checking || selected.size === 0}>
								{isApplying ? (
									<LoaderCircleIcon className="size-4 me-1.5 animate-spin" />
								) : (
									<DownloadIcon className="size-4 me-1.5" />
								)}
								{isApplying ? <Trans>Applying…</Trans> : <Trans>Apply selected</Trans>}
								{!isApplying && ` (${selected.size})`}
							</Button>
						)}
					</div>
				</div>
			</CardHeader>
			{updates.length > 0 && (
				<div
					className={cn(
						"max-h-[calc(100dvh-17rem)] relative overflow-auto border rounded-md",
						isApplying && "opacity-60 pointer-events-none"
					)}
				>
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
										className={update.hd ? "opacity-60" : "cursor-pointer"}
										data-state={selected.has(update.n) ? "selected" : undefined}
										onClick={() => !update.hd && toggleOne(update.n)}
									>
										<TableCell className="px-3 py-2.5">
											<Checkbox
												checked={selected.has(update.n)}
												disabled={update.hd}
												onCheckedChange={() => toggleOne(update.n)}
												aria-label={update.n}
												onClick={(e) => e.stopPropagation()}
											/>
										</TableCell>
										<TableCell className="py-2.5 font-medium">
											{update.n}
											{update.sec && (
												<Badge
													variant="outline"
													className="ms-2 align-middle border-red-500/50 text-red-500"
													title={t`Security update`}
												>
													<Trans>security</Trans>
												</Badge>
											)}
											{update.hd && (
												<Badge
													variant="outline"
													className="ms-2 align-middle"
													title={t`Pinned with apt-mark hold — lift the hold on the system to update`}
												>
													<Trans>Held</Trans>
												</Badge>
											)}
										</TableCell>
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
