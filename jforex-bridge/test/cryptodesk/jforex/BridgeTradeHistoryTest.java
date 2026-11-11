package cryptodesk.jforex;

import com.dukascopy.api.*;
import com.dukascopy.api.system.*;
import com.google.gson.*;
import java.nio.file.*;
import java.util.*;
import java.util.concurrent.*;
import java.util.concurrent.atomic.*;
import static cryptodesk.jforex.BridgeTest.*;

/** Offline SDK interface checks: attribution and close operate on the real bridge path. */
public final class BridgeTradeHistoryTest {
 static void run() throws Exception {
  String intent="abcdef0123456789abcdef0123456789";
  AtomicBoolean connected=new AtomicBoolean(),strategyThread=new AtomicBoolean(),foreign=new AtomicBoolean();
  AtomicReference<IOrder.State> state=new AtomicReference<>(IOrder.State.FILLED);
  AtomicInteger closes=new AtomicInteger(),opens=new AtomicInteger();
  AtomicBoolean accountUnavailable=new AtomicBoolean(),incompleteExits=new AtomicBoolean();
  ICloseOrder firstExit=fake(ICloseOrder.class,(name,args)->switch(name){case "getAmount"->.0004;case "getPrice"->1.095;default->null;});
  ICloseOrder lastExit=fake(ICloseOrder.class,(name,args)->switch(name){case "getAmount"->.0006;case "getPrice"->1.09758;default->null;});
  AtomicLong closedAt=new AtomicLong();long openedAt=System.currentTimeMillis()-1000;
  IFillOrder fill=fake(IFillOrder.class,(name,args)->name.equals("getAmount")?.001:null);
  IOrder order=fake(IOrder.class,(name,args)->switch(name){
   case "getId"->"owned-id";case "getLabel"->foreign.get()?"external-position":"CD_"+intent;
   case "getInstrument"->Instrument.EURUSD;case "isLong"->true;case "getOriginalAmount"->.001;
   case "getAmount"->state.get()==IOrder.State.FILLED?.001:0.0;case "getState"->state.get();
   case "getFillTime"->openedAt;case "getCloseTime"->closedAt.get();case "getOpenPrice"->1.1003;
   case "getProfitLossInPips"->state.get()==IOrder.State.CLOSED?-37.5:125.5;case "getClosePrice"->state.get()==IOrder.State.CLOSED?1.09758:0.0;
   case "getProfitLossInAccountCurrency"->state.get()==IOrder.State.CLOSED?-3.75:12.5;case "getStopLossPrice"->1.09;case "getTakeProfitPrice"->1.12;case "getFillHistory"->List.of(fill);case "getCloseHistory"->state.get()==IOrder.State.CLOSED?(incompleteExits.get()?List.of(lastExit):List.of(firstExit,lastExit)):List.of();
   case "close"->{check(strategyThread.get(),"native order.close must run on the SDK strategy thread");closes.incrementAndGet();closedAt.set(System.currentTimeMillis());state.set(IOrder.State.CLOSED);yield null;}
   default->null;
  });
  IEngine engine=fake(IEngine.class,(name,args)->switch(name){
   case "getOrderById"->args[0].equals("owned-id")&&state.get()==IOrder.State.FILLED?order:null;
   case "getOrder"->state.get()==IOrder.State.FILLED?order:null;
   case "submitOrder"->{opens.incrementAndGet();throw new AssertionError("history and close must never submit a new position");}
   default->null;
  });
  IHistory history=fake(IHistory.class,(name,args)->switch(name){case "getHistoricalOrderById"->args[0].equals("owned-id")&&state.get()==IOrder.State.CLOSED?order:null;case "getOrdersHistory"->List.of(order);default->null;});
  IAccount account=fake(IAccount.class,(name,args)->{if(accountUnavailable.get())throw new IllegalStateException("Account currency temporarily missing");return name.equals("getAccountCurrency")?JFCurrency.getInstance("USD"):null;});
  IContext context=fake(IContext.class,(name,args)->switch(name){case "getEngine"->engine;case "getHistory"->history;case "getAccount"->account;
   case "executeTask"->{FutureTask<Object> task=new FutureTask<>((Callable<Object>)args[0]);strategyThread.set(true);try{task.run();}finally{strategyThread.set(false);}yield task;}
   default->null;
  });
  IClient client=fake(IClient.class,(name,args)->switch(name){case "isConnected"->connected.get();case "connect"->{connected.set(true);yield null;}case "getAvailableInstruments"->Set.of(Instrument.EURUSD);case "startStrategy"->{((IStrategy)args[0]).onStart(context);yield 1L;}default->null;});
  try(Bridge bridge=new Bridge(()->client,event->{},100)){
   JsonObject login=request("connect");login.addProperty("username","offline-history-user");login.addProperty("password","offline-history-password");login.addProperty("environment","demo");login.addProperty("cacheDirectory",Files.createTempDirectory("jforex-history-offline-").toString());good(bridge,login);
   JsonObject lookup=BridgeTradingTest.trade("tradeHistory",Instrument.EURUSD,"buy",1000,1.09,1.12);lookup.addProperty("orderID","owned-id");lookup.addProperty("from",openedAt-1000);
   JsonObject found=good(bridge,lookup).getAsJsonObject("result");check(found.get("openDate").getAsLong()==openedAt&&found.get("filledQuantity").getAsDouble()==1000,"SDK fill time and base units retained");check(found.get("accountCurrency").getAsString().equals("USD")&&found.get("profitPerPriceUnit").getAsDouble()==1&&found.get("profitLoss").getAsDouble()==12.5,"history recovers same-currency risk conversion and native open P/L without a saved preview");
   check(found.get("profitLossPips").getAsDouble()==125.5&&found.get("pipSize").getAsDouble()==.0001&&!found.has("exitPrice"),"native current pips use instrument pip size; an open position has no exit price");
   accountUnavailable.set(true);JsonObject noCurrency=good(bridge,lookup).getAsJsonObject("result");check(noCurrency.has("profitLossError")&&noCurrency.get("profitLossPips").getAsDouble()==125.5,"native pips remain available independently of account currency/FX");accountUnavailable.set(false);
   JsonObject fast=lookup.deepCopy();fast.addProperty("includeValuation",false);JsonObject fastResult=good(bridge,fast).getAsJsonObject("result");check(!fastResult.has("profitPerPriceUnit")&&fastResult.get("profitLoss").getAsDouble()==12.5,"close ownership refresh skips advisory conversion and keeps native P/L");
   JsonObject legacy=lookup.deepCopy();legacy.remove("orderID");check(good(bridge,legacy).getAsJsonObject("result").get("orderID").getAsString().equals("owned-id"),"legacy local claims recover an open order by exact CD_ label");
   foreign.set(true);check(!bridge.handle(lookup).get("ok").getAsBoolean(),"an external order label cannot become a platform trade");foreign.set(false);
   JsonObject close=lookup.deepCopy();close.addProperty("op","tradeClose");close.addProperty("closeIntent","bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb");close.addProperty("remainingQuantity",500);
   check(!bridge.handle(close).get("ok").getAsBoolean()&&closes.get()==0,"changed remaining quantity must not be closed");
   close.addProperty("closeIntent","cccccccccccccccccccccccccccccccc");close.addProperty("remainingQuantity",1000);
   JsonObject result=good(bridge,close).getAsJsonObject("result");check(result.get("status").getAsString().equals("closed")&&result.get("closeDate").getAsLong()==closedAt.get()&&closes.get()==1,"exact native position closed and server close time returned");
   check(!bridge.handle(close).get("ok").getAsBoolean()&&closes.get()==1,"close confirmation is single-use");
   found=good(bridge,lookup).getAsJsonObject("result");check(found.get("closeDate").getAsLong()==closedAt.get(),"closed position looked up through SDK history");check(found.get("profitLoss").getAsDouble()==-3.75,"closed SDK profit/loss is preserved as reported");check(found.get("profitLossPips").getAsDouble()==-37.5&&Math.abs(found.get("exitPrice").getAsDouble()-1.096548)<1e-12,"final SDK pips and quantity-weighted exit cover both partial closes rather than using the last close price");incompleteExits.set(true);check(!good(bridge,lookup).getAsJsonObject("result").has("exitPrice"),"incomplete closing fills cannot invent a full-trade exit price");incompleteExits.set(false);check(good(bridge,legacy).getAsJsonObject("result").get("closeDate").getAsLong()==closedAt.get(),"legacy local claims recover a closed order by exact label through SDK history");check(opens.get()==0,"history/close created no new entry order");
  }
  System.out.println("JForex platform-only history, exact-position close and no-repeat checks passed");
 }
}
