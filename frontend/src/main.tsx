import {showStartupFailure} from './components/StartupFailure';

// The HTML shell stays visible while the application modules and styles load.
// Load React with the application; this entry stays small enough to report an
// import failure without needing the charting UI or its styles to be available.
const host=document.getElementById('root')!;
async function start(){
 try{
  // Test-only bridge is never enabled without an explicit environment flag.
  if(import.meta.env.VITE_EXPLICIT_TEST_BRIDGE==='1'){const {installTestBridge}=await import('./test/browserBridge');await installTestBridge();}
  const {mountApplication}=await import('./Application');
  mountApplication(host);
 }catch{
  showStartupFailure(host);
 }
}
await start();
