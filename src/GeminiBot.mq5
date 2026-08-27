//+------------------------------------------------------------------+
//|                                                   GeminiBot.mq5 |
//|                               Copyright 2026, google.com.jules   |
//|                         Antigravity Gemini 3.7 & Exness HA EA    |
//+------------------------------------------------------------------+
#property copyright "Copyright 2026, google.com.jules"
#property link      "https://github.com/Mouy-leng172/Gemini-3.6-flash-Exness-f52ca206"
#property version   "1.00"
#property description "Up-to-date MetaTrader 5 Expert Advisor powered by Antigravity Gemini 3.7 for Exness Live Trading."

#include <Trade\Trade.mqh>

//--- Input Parameters
input group "=== Trading Settings ==="
input double   InpLotSize         = 0.01;      // Lot Size
input int      InpStopLoss        = 150;       // Stop Loss (in points)
input int      InpTakeProfit      = 300;       // Take Profit (in points)
input int      InpTrailingStop    = 100;       // Trailing Stop (in points)
input int      InpTrailingStep    = 20;        // Trailing Step (in points)
input ulong    InpMagicNumber     = 3637001;   // EA Magic Number

input group "=== Indicator & Signal Settings ==="
input int      InpMAPeriodFast    = 10;        // Fast Moving Average Period
input int      InpMAPeriodSlow    = 20;        // Slow Moving Average Period
input ENUM_MA_METHOD InpMAMethod  = MODE_EMA;  // Moving Average Method

//--- Global Objects & Handles
CTrade         ExtTrade;
int            ExtHandleFastMA    = INVALID_HANDLE;
int            ExtHandleSlowMA    = INVALID_HANDLE;

//+------------------------------------------------------------------+
//| Expert initialization function                                   |
//+------------------------------------------------------------------+
int OnInit()
{
   // Set Magic Number for trade operations
   ExtTrade.SetExpertMagicNumber(InpMagicNumber);
   ExtTrade.SetMarginMode();

   // Initialize Indicators
   ExtHandleFastMA = iMA(_Symbol, _Period, InpMAPeriodFast, 0, InpMAMethod, PRICE_CLOSE);
   ExtHandleSlowMA = iMA(_Symbol, _Period, InpMAPeriodSlow, 0, InpMAMethod, PRICE_CLOSE);

   if(ExtHandleFastMA == INVALID_HANDLE || ExtHandleSlowMA == INVALID_HANDLE)
   {
      Print("Error initializing Moving Average indicators.");
      return(INIT_FAILED);
   }

   Print("GeminiBot Expert Advisor initialized successfully for symbol: ", _Symbol);
   return(INIT_SUCCEEDED);
}

//+------------------------------------------------------------------+
//| Expert deinitialization function                                 |
//+------------------------------------------------------------------+
void OnDeinit(const int reason)
{
   if(ExtHandleFastMA != INVALID_HANDLE) IndicatorRelease(ExtHandleFastMA);
   if(ExtHandleSlowMA != INVALID_HANDLE) IndicatorRelease(ExtHandleSlowMA);
   Print("GeminiBot Expert Advisor deinitialized. Reason code: ", reason);
}

//+------------------------------------------------------------------+
//| Expert tick function                                             |
//+------------------------------------------------------------------+
void OnTick()
{
   // Trailing stop execution
   ApplyTrailingStop();

   // Check if new bar opened or trade condition needs checking
   static datetime lastBarTime = 0;
   datetime currentBarTime = iTime(_Symbol, _Period, 0);
   if(currentBarTime == lastBarTime) return;
   lastBarTime = currentBarTime;

   // Check open positions count for this EA
   int totalPositions = 0;
   for(int i = PositionsTotal() - 1; i >= 0; i--)
   {
      if(PositionGetSymbol(i) == _Symbol && PositionGetInteger(POSITION_MAGIC) == InpMagicNumber)
      {
         totalPositions++;
      }
   }

   if(totalPositions > 0) return; // Allow 1 active position per symbol/magic

   // Read MA Values
   double fastMA[2], slowMA[2];
   if(CopyBuffer(ExtHandleFastMA, 0, 1, 2, fastMA) < 2) return;
   if(CopyBuffer(ExtHandleSlowMA, 0, 1, 2, slowMA) < 2) return;

   // Signal Check: Bullish Crossover (BUY)
   if(fastMA[0] <= slowMA[0] && fastMA[1] > slowMA[1])
   {
      double ask = SymbolInfoDouble(_Symbol, SYMBOL_ASK);
      double sl  = (InpStopLoss > 0) ? ask - InpStopLoss * _Point : 0;
      double tp  = (InpTakeProfit > 0) ? ask + InpTakeProfit * _Point : 0;

      Print("Bullish signal detected. Opening BUY position on ", _Symbol);
      ExtTrade.Buy(InpLotSize, _Symbol, ask, sl, tp, "GeminiBot Buy");
   }
   // Signal Check: Bearish Crossover (SELL)
   else if(fastMA[0] >= slowMA[0] && fastMA[1] < slowMA[1])
   {
      double bid = SymbolInfoDouble(_Symbol, SYMBOL_BID);
      double sl  = (InpStopLoss > 0) ? bid + InpStopLoss * _Point : 0;
      double tp  = (InpTakeProfit > 0) ? bid - InpTakeProfit * _Point : 0;

      Print("Bearish signal detected. Opening SELL position on ", _Symbol);
      ExtTrade.Sell(InpLotSize, _Symbol, bid, sl, tp, "GeminiBot Sell");
   }
}

//+------------------------------------------------------------------+
//| Trailing Stop Logic                                              |
//+------------------------------------------------------------------+
void ApplyTrailingStop()
{
   if(InpTrailingStop <= 0) return;

   for(int i = PositionsTotal() - 1; i >= 0; i--)
   {
      if(PositionGetSymbol(i) == _Symbol && PositionGetInteger(POSITION_MAGIC) == InpMagicNumber)
      {
         ulong ticket = PositionGetInteger(POSITION_TICKET);
         ENUM_POSITION_TYPE type = (ENUM_POSITION_TYPE)PositionGetInteger(POSITION_TYPE);
         double currentSL = PositionGetDouble(POSITION_SL);
         double openPrice = PositionGetDouble(POSITION_PRICE_OPEN);

         if(type == POSITION_TYPE_BUY)
         {
            double bid = SymbolInfoDouble(_Symbol, SYMBOL_BID);
            if(bid - openPrice > InpTrailingStop * _Point)
            {
               double newSL = bid - InpTrailingStop * _Point;
               if(newSL > currentSL + InpTrailingStep * _Point)
               {
                  ExtTrade.PositionModify(ticket, newSL, PositionGetDouble(POSITION_TP));
                  Print("Trailing Stop updated for BUY position #", ticket, " New SL: ", newSL);
               }
            }
         }
         else if(type == POSITION_TYPE_SELL)
         {
            double ask = SymbolInfoDouble(_Symbol, SYMBOL_ASK);
            if(openPrice - ask > InpTrailingStop * _Point)
            {
               double newSL = ask + InpTrailingStop * _Point;
               if(currentSL == 0 || newSL < currentSL - InpTrailingStep * _Point)
               {
                  ExtTrade.PositionModify(ticket, newSL, PositionGetDouble(POSITION_TP));
                  Print("Trailing Stop updated for SELL position #", ticket, " New SL: ", newSL);
               }
            }
         }
      }
   }
}
