import { t } from "@lingui/core/macro"
import { Trans } from "@lingui/react/macro"
import { RotateCcwIcon } from "lucide-react"
import { memo, useState } from "react"
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
import { Button, buttonVariants } from "@/components/ui/button"
import { toast } from "@/components/ui/use-toast"
import { isReadOnlyUser, pb } from "@/lib/api"
import { cn } from "@/lib/utils"
import type { SystemRecord } from "@/types"

/**
 * A red reboot badge shown on a system tile only when the agent reports a
 * pending reboot (Info.rb, e.g. after a kernel/libc update). Clicking it
 * confirms and reboots the host. Hidden for readonly users.
 */
export default memo(function RebootButton({ system }: { system: SystemRecord }) {
	const [open, setOpen] = useState(false)

	if (!system.info?.rb || isReadOnlyUser()) {
		return null
	}

	return (
		<>
			<Button
				variant="ghost"
				size="icon"
				className="text-red-500 hover:text-red-500 relative z-10"
				aria-label={t`Reboot required`}
				title={t`Reboot required to finish updates`}
				data-nolink
				onClick={() => setOpen(true)}
			>
				<RotateCcwIcon className="size-4" />
			</Button>
			<AlertDialog open={open} onOpenChange={setOpen}>
				<AlertDialogContent>
					<AlertDialogHeader>
						<AlertDialogTitle>
							<Trans>Reboot {system.name}?</Trans>
						</AlertDialogTitle>
						<AlertDialogDescription>
							<Trans>
								This system needs a reboot to finish applying updates. It will show as down for a short time while
								it comes back up.
							</Trans>
						</AlertDialogDescription>
					</AlertDialogHeader>
					<AlertDialogFooter>
						<AlertDialogCancel>
							<Trans>Cancel</Trans>
						</AlertDialogCancel>
						<AlertDialogAction
							className={cn(buttonVariants({ variant: "destructive" }))}
							onClick={async () => {
								try {
									await pb.send(`/api/beszel-ext/systems/${system.id}/reboot`, { method: "POST", requestKey: null })
									toast({ title: t`Rebooting`, description: t`${system.name} is restarting now` })
								} catch (error: any) {
									toast({ title: t`Reboot failed`, description: error?.message })
								}
							}}
						>
							<Trans>Reboot</Trans>
						</AlertDialogAction>
					</AlertDialogFooter>
				</AlertDialogContent>
			</AlertDialog>
		</>
	)
})
