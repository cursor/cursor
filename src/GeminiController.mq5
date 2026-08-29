//+------------------------------------------------------------------+
//|                                             GeminiController.mq5 |
//|                               Copyright 2026, google.com.jules   |
//|                 Master EA to orchestrate multiple strategies     |
//+------------------------------------------------------------------+
#property copyright "Copyright 2026, google.com.jules"
#property link      "https://github.com/Mouy-leng172/Gemini-3.6-flash-Exness-f52ca206"
#property version   "1.00"
#property description "Modular Master Controller Expert Advisor for Antigravity Gemini 3.7 & Exness MT5 HA."
#property strict

// Include shared utilities
#include <Trade\Trade.mqh>
#include "RiskManager.mqh"
#include "Logger.mqh"

// Forward declarations of strategy modules
#include "Strategy_MA.mqh"
#include "Strategy_Breakout.mqh"
#include "Strategy_TrailingStop.mqh"

// Global objects
CTrade trade;
RiskManager risk;
Logger logger;

// Strategy instances
Strategy_MA maStrategy;
Strategy_Breakout breakoutStrategy;
Strategy_TrailingStop trailingStrategy;

// Input parameters
input group "=== Strategy Activation Controls ==="
input bool UseMAStrategy       = true;     // Enable Moving Average Crossover Strategy
input bool UseBreakoutStrategy = false;    // Enable Donchian Breakout Strategy
input bool UseTrailingStrategy = true;     // Enable Trailing Stop Execution

input group "=== Risk Management Settings ==="
input double MaxDrawdownPct    = 10.0;     // Max Drawdown Threshold (%)
input double MaxDailyLoss      = 100.0;    // Max Daily Loss Limit ($)

//+------------------------------------------------------------------+
//| Expert initialization function                                   |
//+------------------------------------------------------------------+
int OnInit()
{
   logger.Init("GeminiController.log");
   logger.Write("GeminiController Master EA initialized.");

   risk.Init(MaxDrawdownPct, MaxDailyLoss);

   maStrategy.Init();
   breakoutStrategy.Init();
   trailingStrategy.Init();

   return(INIT_SUCCEEDED);
}

//+------------------------------------------------------------------+
//| Expert deinitialization function                                 |
//+------------------------------------------------------------------+
void OnDeinit(const int reason)
{
   logger.Write("GeminiController Master EA stopped. Reason: " + IntegerToString(reason));
}

//+------------------------------------------------------------------+
//| Expert tick function                                             |
//+------------------------------------------------------------------+
void OnTick()
{
   if(!risk.IsTradeAllowed())
      return;

   if(UseMAStrategy)
      maStrategy.Run();

   if(UseBreakoutStrategy)
      breakoutStrategy.Run();

   if(UseTrailingStrategy)
      trailingStrategy.Run();
}
