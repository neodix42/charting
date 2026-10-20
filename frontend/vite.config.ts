import { defineConfig } from 'vite';
import {appVersion} from './build-version.mjs';
export default defineConfig({base:'./',build:{target:'es2022'},define:{__CRYPTODESK_VERSION__:JSON.stringify(appVersion)},plugins:[{
 name:'cryptodesk-build-version',
 generateBundle(){this.emitFile({type:'asset',fileName:'app-version.json',source:JSON.stringify({version:appVersion,tag:`v${appVersion}`})+'\n'});}
}]});
