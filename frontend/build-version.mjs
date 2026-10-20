import {execFileSync} from 'node:child_process';
import {fileURLToPath} from 'node:url';

// Share the release planner and packagers' validated version source.
export const appVersion=execFileSync('python3',[fileURLToPath(new URL('../scripts/app_version.py',import.meta.url))],{encoding:'utf8'}).trim();
