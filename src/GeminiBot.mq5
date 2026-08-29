//+------------------------------------------------------------------+
//|                                                    GeminiBot.mq5 |
//|                               Copyright 2026, google.com.jules   |
//|                                  https://github.com/Mouy-leng172 |
//+------------------------------------------------------------------+
#property copyright "Copyright 2026, google.com.jules"
#property link      "https://github.com/Mouy-leng172/Gemini-3.6-flash-Exness-f52ca206"
#property version   "1.00"
#property description "Antigravity Gemini 3.7 Expert Advisor for Exness MT5 Live Execution"

// Include Standard Trade Library
#include <Trade\Trade.mqh>

// Input Parameters
input group "=== Risk Management ==="
input double TargetLotSize   = 0.01;      // Trading Lot Size
input double StopLossPips    = 50.0;      // Stop Loss in Pips
input double TakeProfitPips  = 100.0;     // Take Profit in Pips
input double TrailingStopPips= 30.0;      // Trailing Stop in Pips

input group "=== Technical Indicators ==="
input int    FastMAPeriod    = 10;        // Fast Moving Average Period
input int    SlowMAPeriod    = 30;        // Slow Moving Average Period

// Global Objects
CTrade trade;
int fastHandle;
int slowHandle;

//+------------------------------------------------------------------+
//| Expert initialization function                                   |
//+------------------------------------------------------------------+
int OnInit()
{
   Print("Initializing Antigravity Gemini 3.7 EA for Exness MetaTrader 5...");

   // Initialize Indicator Handles
   fastHandle = iMA(_Symbol, _Period, FastMAPeriod, 0, MODE_EMA, PRICE_CLOSE);
   slowHandle = iMA(_Symbol, _Period, SlowMAPeriod, 0, MODE_EMA, PRICE_CLOSE);

   if(fastHandle == INVALID_HANDLE || slowHandle == INVALID_HANDLE)
   {
      Print("Error creating indicator handles.");
      return(INIT_FAILED);
   }

   Print("Antigravity Gemini 3.7 EA initialized successfully.");
   return(INIT_SUCCEEDED);
}

//+------------------------------------------------------------------+
//| Expert deinitialization function                                 |
//+------------------------------------------------------------------+
void OnDeinit(const int reason)
{
   Print("Deinitializing Antigravity Gemini 3.7 EA. Reason code: ", reason);
   IndicatorRelease(fastHandle);
   IndicatorRelease(slowHandle);
}

//+------------------------------------------------------------------+
//| Expert tick function                                             |
//+------------------------------------------------------------------+
void OnTick()
{
   // Copy Moving Average Values
   double fastMA[], slowMA[];
   ArraySetAsSeries(fastMA, true);
   ArraySetAsSeries(slowMA, true);

   if(CopyBuffer(fastHandle, 0, 0, 3, fastMA) < 3 || CopyBuffer(slowHandle, 0, 0, 3, slowMA) < 3)
   {
      return;
   }

   // Check for Open Positions
   int totalPositions = PositionsTotal();

   // Signal Logic: Fast MA crosses Slow MA
   bool buySignal  = (fastMA[2] <= slowMA[2]) && (fastMA[1] > slowMA[1]);
   bool sellSignal = (fastMA[2] >= slowMA[2]) && (fastMA[1] < slowMA[1]);

   double ask = SymbolInfoDouble(_Symbol, SYMBOL_ASK);
   double bid = SymbolInfoDouble(_Symbol, SYMBOL_BID);
   double point = SymbolInfoDouble(_Symbol, SYMBOL_POINT);

   if(totalPositions == 0)
   {
      if(buySignal)
      {
         double sl = ask - (StopLossPips * point * 10);
         double tp = ask + (TakeProfitPips * point * 10);
         Print("Fast EMA crossed above Slow EMA. Executing Buy Order...");
         trade.Buy(TargetLotSize, _Symbol, ask, sl, tp, "Gemini 3.7 Buy");
      }
      else if(sellSignal)
      {
         double sl = bid + (StopLossPips * point * 10);
         double tp = bid - (TakeProfitPips * point * 10);
         Print("Fast EMA crossed below Slow EMA. Executing Sell Order...");
         trade.Sell(TargetLotSize, _Symbol, bid, sl, tp, "Gemini 3.7 Sell");
      }
   }
}
