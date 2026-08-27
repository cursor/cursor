//+------------------------------------------------------------------+
//|                                                       Logger.mqh |
//|                               Copyright 2026, google.com.jules   |
//|                              Shared Logging Utility for MQL5 EAs |
//+------------------------------------------------------------------+
#property copyright "Copyright 2026, google.com.jules"
#property strict

class Logger
{
private:
   string m_fileName;
   int    m_fileHandle;

public:
   Logger() : m_fileName("GeminiController.log"), m_fileHandle(INVALID_HANDLE) {}
   ~Logger()
   {
      if(m_fileHandle != INVALID_HANDLE)
         FileClose(m_fileHandle);
   }

   void Init(string fileName = "GeminiController.log")
   {
      m_fileName = fileName;
      m_fileHandle = FileOpen(m_fileName, FILE_WRITE|FILE_READ|FILE_TXT|FILE_SHARED_WRITE, ';');
      if(m_fileHandle != INVALID_HANDLE)
         FileSeek(m_fileHandle, 0, SEEK_END);
   }

   void Write(string message)
   {
      string timeStr = TimeToString(TimeCurrent(), TIME_DATE|TIME_SECONDS);
      string entry = "[" + timeStr + "] " + message;
      Print(entry);

      if(m_fileHandle != INVALID_HANDLE)
      {
         FileWriteString(m_fileHandle, entry + "\r\n");
         FileFlush(m_fileHandle);
      }
   }
};
