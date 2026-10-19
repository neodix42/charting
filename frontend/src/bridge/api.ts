import type {Backend, Runtime} from '../types';
export function getBridge():{api:Backend;runtime:Runtime}{const api=window.go?.main?.App,runtime=window.runtime;if(!api||!runtime)throw Error('The Wails desktop bridge is unavailable. Launch the CryptoDesk desktop application. Browser preview cannot load market data or persist a workspace.');return {api,runtime};}
