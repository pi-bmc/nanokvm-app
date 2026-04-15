import { useEffect, useState } from 'react';
import { useAtom, useAtomValue, useSetAtom } from 'jotai';

import { Head } from '@/components/head.tsx';
import {
  serialConfigAtom,
  serialConnectCountAtom,
  serialTerminalOpenAtom
} from '@/jotai/serial.ts';
import * as api from '@/api/vm';

import { SerialTerminalPane } from './serial-terminal-pane';

export const Desktop = () => {
  const [serialConfig] = useAtom(serialConfigAtom);
  const setSerialTerminalOpen = useSetAtom(serialTerminalOpenAtom);
  const setConnectCount = useSetAtom(serialConnectCountAtom);
  const isSerialTerminalOpen = useAtomValue(serialTerminalOpenAtom);

  const [isPowerOn, setIsPowerOn] = useState(false);
  const [isLoading, setIsLoading] = useState(false);

  useEffect(() => {
    setSerialTerminalOpen(true);
    setConnectCount((c: number) => c + 1);

    getLed();
    const interval = setInterval(getLed, 5000);
    return () => clearInterval(interval);
  }, []);

  async function getLed() {
    try {
      const rsp = await api.getGpio();
      if (rsp.code === 0) setIsPowerOn(rsp.data.pwr);
    } catch (_) {}
  }

  async function handlePower(type: string, duration: number) {
    setIsLoading(true);
    try {
      await api.setGpio(type, duration);
    } catch (_) {}
    setTimeout(() => {
      getLed();
      setIsLoading(false);
    }, 1500);
  }

  function reconnectSerial() {
    setSerialTerminalOpen(true);
    setConnectCount((c: number) => c + 1);
  }

  return (
    <div className="flex h-screen w-screen flex-col bg-neutral-950 text-white">
      <Head title="NanoKVM BMC" />

      {/* Top bar */}
      <div className="flex h-10 shrink-0 items-center justify-between border-b border-neutral-800 px-4">
        <div className="flex items-center space-x-4">
          <span className="text-sm font-semibold text-neutral-200">NanoKVM BMC</span>
          <div className="flex items-center space-x-2">
            <div
              className={`h-2.5 w-2.5 rounded-full ${isPowerOn ? 'bg-green-500' : 'bg-red-500'}`}
            />
            <span className="text-xs text-neutral-400">
              {isPowerOn ? 'Power On' : 'Power Off'}
            </span>
          </div>
        </div>

        <div className="flex items-center space-x-2">
          <span className="text-xs text-neutral-500">
            {serialConfig.port} @ {serialConfig.baudrate}
          </span>
          <button
            className="rounded bg-neutral-700 px-2 py-1 text-xs hover:bg-neutral-600"
            onClick={reconnectSerial}
          >
            Reconnect
          </button>
          <div className="mx-2 h-4 w-px bg-neutral-700" />
          <button
            disabled={isLoading}
            className="rounded bg-blue-700 px-2 py-1 text-xs hover:bg-blue-600 disabled:opacity-50"
            onClick={() => handlePower('power', 800)}
            title="Short press power button"
          >
            Power
          </button>
          <button
            disabled={isLoading}
            className="rounded bg-yellow-700 px-2 py-1 text-xs hover:bg-yellow-600 disabled:opacity-50"
            onClick={() => handlePower('reset', 800)}
            title="Press reset button"
          >
            Reset
          </button>
          <button
            disabled={isLoading}
            className="rounded bg-red-700 px-2 py-1 text-xs hover:bg-red-600 disabled:opacity-50"
            onClick={() => handlePower('power', 5000)}
            title="Long press power button (force off)"
          >
            Force Off
          </button>
        </div>
      </div>

      {/* Serial terminal — full remaining height */}
      <div className="min-h-0 flex-1">
        {isSerialTerminalOpen ? (
          <SerialTerminalPane />
        ) : (
          <div className="flex h-full items-center justify-center">
            <button
              className="rounded bg-neutral-800 px-4 py-2 hover:bg-neutral-700"
              onClick={reconnectSerial}
            >
              Connect Serial Console
            </button>
          </div>
        )}
      </div>
    </div>
  );
};
