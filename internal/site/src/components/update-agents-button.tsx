import { t } from "@lingui/core/macro"
import { Trans } from "@lingui/react/macro"
import { DownloadIcon, LoaderCircleIcon } from "lucide-react"
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
import { toast } from "@/components/ui/use-toast"
import { isAdmin, pb } from "@/lib/api"
import { cn } from "@/lib/utils"

/**
 * One-click fleet agent update: pushes the hub's staged fork binary to every
 * "up" system. Each agent verifies the checksum and no-ops if already current,
 * so this is always safe to run.
 */
export function UpdateAgentsButton({ className }: { className?: string }) {
	const [open, setOpen] = useState(false)
	const [running, setRunning] = useState(false)

	if (!isAdmin()) {
		return null
	}

	const updateAll = async () => {
		setRunning(true)
		try {
			const { results } = await pb.send<{ results: Record<string, string> }>("/api/beszel-ext/agents/update-all", {
				method: "POST",
				requestKey: null,
			})
			const entries = Object.entries(results ?? {})
			const updated = entries.filter(([, r]) => r === "updated").map(([n]) => n)
			const current = entries.filter(([, r]) => r === "up to date").length
			const failed = entries.filter(([, r]) => r.startsWith("error")).map(([n, r]) => `${n} (${r.slice(7)})`)
			toast({
				title: t`Agent updates pushed`,
				description: [
					updated.length ? `${t`Updated`}: ${updated.join(", ")}` : "",
					current ? `${t`Already current`}: ${current}` : "",
					failed.length ? `${t`Failed`}: ${failed.join("; ")}` : "",
				]
					.filter(Boolean)
					.join(" · "),
				duration: 10_000,
			})
		} catch (error: any) {
			toast({ title: t`Agent update failed`, description: error?.message })
		} finally {
			setRunning(false)
		}
	}

	return (
		<>
			<Button
				variant="outline"
				size="sm"
				className={cn("flex gap-1.5", className)}
				disabled={running}
				onClick={() => setOpen(true)}
				title={t`Push the hub's agent build to all systems`}
			>
				{running ? <LoaderCircleIcon className="size-4 animate-spin" /> : <DownloadIcon className="size-4" />}
				<span className="hidden sm:inline">
					<Trans>Update agents</Trans>
				</span>
			</Button>
			<AlertDialog open={open} onOpenChange={setOpen}>
				<AlertDialogContent>
					<AlertDialogHeader>
						<AlertDialogTitle>
							<Trans>Update all agents?</Trans>
						</AlertDialogTitle>
						<AlertDialogDescription>
							<Trans>
								Every online system downloads the hub's current agent build, verifies it, and restarts its agent.
								Agents that are already current are skipped. Each system may show as down for a few seconds while
								its agent restarts.
							</Trans>
						</AlertDialogDescription>
					</AlertDialogHeader>
					<AlertDialogFooter>
						<AlertDialogCancel>
							<Trans>Cancel</Trans>
						</AlertDialogCancel>
						<AlertDialogAction onClick={updateAll}>
							<Trans>Update all</Trans>
						</AlertDialogAction>
					</AlertDialogFooter>
				</AlertDialogContent>
			</AlertDialog>
		</>
	)
}
