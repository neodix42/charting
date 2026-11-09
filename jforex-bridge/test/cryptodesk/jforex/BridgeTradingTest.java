package cryptodesk.jforex;

import com.dukascopy.api.*;
import com.dukascopy.api.system.*;
import com.google.gson.*;
import java.lang.reflect.*;
import java.nio.file.*;
import java.util.*;
import java.util.concurrent.*;
import java.util.concurrent.atomic.*;
import static cryptodesk.jforex.BridgeTest.*;

/** Offline interface fakes exercise the real trading validation and SDK dispatch. */
public final class BridgeTradingTest {
    static void metadata(Instrument instrument, String name, double value) throws Exception {
        Field field = Instrument.class.getDeclaredField(name); field.setAccessible(true); field.setDouble(instrument, value);
    }
    static JsonObject trade(String op, Instrument instrument, String side, double quantity, double sl, double tp) {
        JsonObject request=request(op);request.addProperty("instrument",instrument.name());request.addProperty("side",side);
        request.addProperty("quantity",quantity);request.addProperty("stopLoss",sl);request.addProperty("takeProfit",tp);request.addProperty("leverage",0);
        request.addProperty("intent","abcdef0123456789abcdef0123456789");return request;
    }
    public static void main(String[] args) throws Exception {
        Instrument fx=Instrument.EURUSD, cfd=Instrument.AAPLUSUSD;
        metadata(fx,"amountPerContract",100_000);metadata(fx,"minTradeAmount",0.01);metadata(fx,"tradeAmountIncrement",0.01);metadata(fx,"maxTradeAmount",1000);
        metadata(cfd,"amountPerContract",1);metadata(cfd,"minTradeAmount",1);metadata(cfd,"tradeAmountIncrement",1);metadata(cfd,"maxTradeAmount",1000);
        AtomicBoolean connected=new AtomicBoolean(), strategyThread=new AtomicBoolean(), protection=new AtomicBoolean(true);
        AtomicReference<Object[]> submitted=new AtomicReference<>();AtomicInteger calls=new AtomicInteger(),valuationCalls=new AtomicInteger();
        AtomicReference<ITick> quote=new AtomicReference<>(fake(ITick.class,(method,values)->switch(method){case "getTime"->System.currentTimeMillis();case "getAsk"->1.1002;case "getBid"->1.1;default->null;}));
        IEngine engine=fake(IEngine.class,(method,values)->{
            if (!method.equals("submitOrder"))return null;
            check(strategyThread.get(),"submitOrder must execute on strategy thread"); calls.incrementAndGet(); submitted.set(values);
            check(values[1] instanceof Instrument,"explicit Instrument overload uses verified million-unit conversion");
            return fake(IOrder.class,(name,input)->switch(name){case "getId"->"broker-order-1";case "getState"->IOrder.State.FILLED;
                case "getAmount"->(Double)values[3];case "getOpenPrice"->1.1003;case "getStopLossPrice"->protection.get()?values[6]:0.0;case "getTakeProfitPrice"->values[7];default->null;});
        });
        IHistory history=fake(IHistory.class,(method,values)->method.equals("getLastTick")?quote.get():null);
        IAccount account=fake(IAccount.class,(method,values)->switch(method){case "getLeverage"->100.0;case "getAccountCurrency"->JFCurrency.getInstance("USD");default->null;});
        JFUtils utils=fake(JFUtils.class,(method,values)->{if(method.equals("convertPipToCurrency")){valuationCalls.incrementAndGet();return ((Instrument)values[0]).getPipValue();}return null;});
        IContext context=fake(IContext.class,(method,values)->switch(method){
            case "getEngine"->engine;case "getHistory"->history;case "getAccount"->account;case "getUtils"->utils;
            case "executeTask"->{FutureTask<Object> task=new FutureTask<>((Callable<Object>)values[0]);strategyThread.set(true);try{task.run();}finally{strategyThread.set(false);}yield task;}
            default->null;
        });
        IClient client=fake(IClient.class,(method,values)->switch(method){
            case "isConnected"->connected.get();case "connect"->{connected.set(true);yield null;}case "getAvailableInstruments"->Set.of(fx,cfd);
            case "startStrategy"->{((IStrategy)values[0]).onStart(context);yield 1L;}default->null;
        });
        try(Bridge bridge=new Bridge(()->client,event->{},100)){
            JsonObject login=request("connect");login.addProperty("username","offline-user");login.addProperty("password","offline-password");login.addProperty("environment","demo");login.addProperty("cacheDirectory",Files.createTempDirectory("jforex-trade-offline-").toString());good(bridge,login);
            JsonObject preview=good(bridge,trade("tradePreview",fx,"buy",1000,1.09,1.12)).getAsJsonObject("result");
            check(preview.get("minQuantity").getAsDouble()==1000 && preview.get("quantityStep").getAsDouble()==1000,"FX contracts convert to base units");
            check(preview.get("entryPrice").getAsDouble()==1.1002 && preview.get("accountLeverage").getAsDouble()==100,"Buy uses ask and fixed account leverage");
            check(calls.get()==0,"preview must not submit orders");
            for(JsonObject invalid:List.of(trade("tradePreview",fx,"buy",100,1.09,1.12),trade("tradePreview",fx,"buy",1500,1.09,1.12),trade("tradePreview",fx,"buy",1000,1.2,1.12),trade("tradePreview",fx,"sell",1000,1.09,1.12))){check(!bridge.handle(invalid).get("ok").getAsBoolean(),"invalid trade rejected before submission");}
            JsonObject leverage=trade("tradePreview",fx,"buy",1000,1.09,1.12);leverage.addProperty("leverage",5);check(!bridge.handle(leverage).get("ok").getAsBoolean(),"cannot change account leverage perorder");
            int conversionsBefore=valuationCalls.get();
            JsonObject order=trade("tradeSubmit",fx,"buy",1000,1.09,1.12);
            JsonObject result=good(bridge,order).getAsJsonObject("result");check(result.get("status").getAsString().equals("filled")&&result.get("protectionConfirmed").getAsBoolean(),"filled with attached protection confirmed");
            Object[] sent=submitted.get();check((Double)sent[3]==0.001 && (Double)sent[4]==0 && (Double)sent[6]==1.09 && (Double)sent[7]==1.12,"SDK market size millions, price0 and attached SL/TP exact");
            check(!bridge.handle(order).get("ok").getAsBoolean()&&calls.get()==1,"same intent never submitted twice");
            JsonObject stock=trade("tradeSubmit",cfd,"sell",2,1.12,1.09);stock.addProperty("intent","bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb");
            protection.set(false);result=good(bridge,stock).getAsJsonObject("result");check(result.get("status").getAsString().equals("partial")&&!result.get("protectionConfirmed").getAsBoolean(),"missing protective level reported partial");
            check((Double)submitted.get()[3]==0.000002,"CFD amount based on actual units, not forex lot assumption");
            check(valuationCalls.get()==conversionsBefore,"order submission does not run advisory FX valuation");
            check(good(bridge,trade("tradePreview",fx,"sell",1000,1.12,1.09)).getAsJsonObject("result").get("entryPrice").getAsDouble()==1.1,"Sell uses bid");
        }
        BridgeValuationTest.run();
        System.out.println("JForex trading validation, SDK thread, amount conversion and no-repeat tests passed");
    }
}
