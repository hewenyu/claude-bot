/**
 * claude-bot loop for pi, installed by `claude-bot init --pi`.
 *
 *   /loop [pause] <prompt>   send prompt now, and again each time the agent
 *                            has settled, after pause (30s, 5m, 1h; default 1m)
 *   /loop stop               stop looping (so does aborting a run with Esc)
 *   /loop                    show what is looping
 *
 * Watch the project with:  /loop /skill:bot
 */
import type { ExtensionAPI, ExtensionContext } from "@earendil-works/pi-coding-agent";

const defaultPause = 60_000;
const unitMs: Record<string, number> = { s: 1_000, m: 60_000, h: 3_600_000 };

export default function (pi: ExtensionAPI) {
	let loop: { prompt: string; pause: number } | undefined;
	let timer: ReturnType<typeof setTimeout> | undefined;

	const cancel = () => {
		clearTimeout(timer);
		timer = undefined;
	};

	const stop = () => {
		cancel();
		loop = undefined;
	};

	// A run of the person's own, started meanwhile, is left alone: its end
	// schedules the next round.
	const send = (ctx: ExtensionContext) => {
		timer = undefined;
		if (loop && ctx.isIdle()) {
			pi.sendUserMessage(loop.prompt, { expandPromptTemplates: true });
		}
	};

	pi.registerCommand("loop", {
		description: "Re-send a prompt each time the agent settles: /loop [30s|5m|1h] <prompt>, /loop stop",
		handler: async (args, ctx) => {
			const text = args.trim();

			if (text === "") {
				ctx.ui.notify(loop ? `looping every ${loop.pause / 1000}s: ${loop.prompt}` : "not looping", "info");
				return;
			}

			if (text === "stop") {
				stop();
				ctx.ui.notify("loop stopped", "info");
				return;
			}

			const [first, ...rest] = text.split(/\s+/);
			const interval = /^(\d+)([smh])$/.exec(first);

			cancel();
			loop =
				interval && rest.length > 0 && Number(interval[1]) > 0
					? { prompt: rest.join(" "), pause: Number(interval[1]) * unitMs[interval[2]] }
					: { prompt: text, pause: defaultPause };

			ctx.ui.notify(`looping every ${loop.pause / 1000}s: ${loop.prompt} (/loop stop to end)`, "info");
			send(ctx);
		},
	});

	pi.on("agent_settled", (event, ctx) => {
		if (!loop) {
			return;
		}

		if (event.aborted) {
			stop();
			ctx.ui.notify("loop stopped: the run was aborted", "warning");
			return;
		}

		cancel();
		timer = setTimeout(() => send(ctx), loop.pause);
	});

	pi.on("session_shutdown", () => stop());
}
