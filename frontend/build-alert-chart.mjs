import {build} from 'vite';
import {fileURLToPath} from 'node:url';
import path from 'node:path';

const root=path.dirname(fileURLToPath(import.meta.url));
await build({
 configFile:false,root,
 define:{'process.env.NODE_ENV':JSON.stringify('production')},
 build:{
  target:'es2022',outDir:path.resolve(root,'../internal/chartimage/assets'),emptyOutDir:false,
  lib:{entry:path.resolve(root,'src/alerts/chartSnapshot.tsx'),name:'CryptoDeskSignalChart',formats:['iife'],fileName:()=> 'chart.js',cssFileName:'chart'},
  cssCodeSplit:false,minify:true,
 },
});
