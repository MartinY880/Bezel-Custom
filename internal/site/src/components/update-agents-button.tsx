import { t } from "@lingui/core/macro"
import { Trans } from "@lingui/react/macro"
import { ChevronDownIcon, DownloadIcon, LoaderCircleIcon, PackageIcon, ShieldAlertIcon } from "lucide-react"
import { useState } from "react"
import {
	AlertDialog,
	AlertDialogAction,
	AlertDialogCancel,
	AlertDialogContent,
	AlertDialogDescription,
	AlertDialogFooter,
	AlertDialogHeader,
	AlertDialogTitle,
} from "@/components/ui/alert-dialog"
import { Button } from "@/components/ui/button"
import {
	DropdownMenu,
	DropdownMenuContent,
	DropdownMenuItem,
	DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { toast } from "@/components/ui/use-toast"
import { isAdmin, pb } from "@/lib/api"
import { cn } from "@/lib/utils"

type FleetAction = "agents" | "updates" | "security"

const actionConfig: Record<
	FleetAction,
	{ url: string; body?: Record<string, unknown>; title: () => string; description: () => string }
> = {
	agents: {
		url: "/api/beszel-ext/agents/update-all",
		title: () => t`Update all agents?`,
		description: () =>
			t`Every online system downloads the hub's current agent build, verifies it, and restarts its agent. Agents that are already current are skipped.`,
	},
	updates: {
		url: "/api/beszel-ext/updates/apply-all",
		body: {},
		title: () => t`Apply ALL package updates on every system?`,
		description: () =>
			t`Every online system with pending updates installs all of them in the background (held packages are skipped). Services on those systems may restart during upgrades.`,
	},
	security: {
		url: "/api/beszel-ext/updates/apply-all",
		body: { securityOnly: true },
		title: () => t`Apply security updates on every system?`,
		description: () =>
			t`Every online system with pending security updates installs them in the background (held packages are skipped).`,
	},
}

/**
 * Fleet-wide actions: update agent binaries everywhere, or start package
 * updates (all / security-only) on every system at once — no drill-down.
 */
export function UpdateAgentsButton({ className }: { className?: string }) {
	const [confirm, setConfirm] = useState<FleetAction | null>(null)
	const [running, setRunning] = useState(false)

	if (!isAdmin()) {
		return null
	}

	const run = async (action: FleetAction) => {
		setRunning(true)
		try {
			const cfg = actionConfig[action]
			const { results } = await pb.send<{ results: Record<string, string> }>(cfg.url, {
				method: "POST",
				body: cfg.body,
				requestKey: null,
			})
			const entries = Object.entries(results ?? {})
			const acted = entries.filter(([, r]) => r.startsWith("updated") || r.startsWith("running")).map(([n]) => n)
			const skipped = entries.filter(([, r]) => r === "up to date" || r === "no updates").length
			const failed = entries.filter(([, r]) => r.startsWith("error")).map(([n, r]) => `${n} (${r.slice(7)})`)
			toast({
				title: action === "agents" ? t`Agent updates pushed` : t`Package updates started`,
				description: [
					acted.length ? `${t`Started`}: ${acted.join(", ")}` : "",
					skipped ? `${t`Nothing to do`}: ${skipped}` : "",
					failed.length ? `${t`Failed`}: ${failed.join("; ")}` : "",
				]
					.filter(Boolean)
					.join(" · "),
				duration: 12_000,
			})
		} catch (error: any) {
			toast({ title: t`Fleet action failed`, description: error?.message })
		} finally {
			setRunning(false)
		}
	}

	return (
		<>
			<DropdownMenu>
				<DropdownMenuTrigger asChild>
					<Button variant="outline" size="sm" className={cn("flex gap-1.5", className)} disabled={running}>
						{running ? <LoaderCircleIcon className="size-4 animate-spin" /> : <PackageIcon className="size-4" />}
						<span className="hidden sm:inline">
							<Trans>Fleet</Trans>
						</span>
						<ChevronDownIcon className="size-3.5 opacity-70" />
					</Button>
				</DropdownMenuTrigger>
				<DropdownMenuContent align="end">
					<DropdownMenuItem onSelect={() => setConfirm("updates")}>
						<PackageIcon className="me-2.5 size-4" />
						<Trans>Apply all updates everywhere</Trans>
					</DropdownMenuItem>
					<DropdownMenuItem onSelect={() => setConfirm("security")}>
						<ShieldAlertIcon className="me-2.5 size-4 text-red-500" />
						<Trans>Apply security updates everywhere</Trans>
					</DropdownMenuItem>
					<DropdownMenuItem onSelect={() => setConfirm("agents")}>
						<DownloadIcon className="me-2.5 size-4" />
						<Trans>Update all agents</Trans>
					</DropdownMenuItem>
				</DropdownMenuContent>
			</DropdownMenu>
			<AlertDialog open={confirm !== null} onOpenChange={(open) => !open && setConfirm(null)}>
				<AlertDialogContent>
					<AlertDialogHeader>
						<AlertDialogTitle>{confirm && actionConfig[confirm].title()}</AlertDialogTitle>
						<AlertDialogDescription>{confirm && actionConfig[confirm].description()}</AlertDialogDescription>
					</AlertDialogHeader>
					<AlertDialogFooter>
						<AlertDialogCancel>
							<Trans>Cancel</Trans>
						</AlertDialogCancel>
						<AlertDialogAction
							onClick={() => {
								if (confirm) run(confirm)
							}}
						>
							<Trans>Continue</Trans>
						</AlertDialogAction>
					</AlertDialogFooter>
				</AlertDialogContent>
			</AlertDialog>
		</>
	)
}
