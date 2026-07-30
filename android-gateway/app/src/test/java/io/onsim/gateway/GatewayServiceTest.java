package io.onsim.gateway;

import static org.junit.Assert.assertArrayEquals;
import static org.junit.Assert.assertEquals;
import static org.junit.Assert.assertThrows;

import org.junit.Test;

public class GatewayServiceTest {
    @Test public void duplicatesMonoSamplesIntoInterleavedStereo() {
        byte[] mono = new byte[]{0x01, 0x02, 0x03, 0x04};
        byte[] stereo = new byte[8];
        GatewayService.monoToStereo(mono, stereo);
        assertArrayEquals(new byte[]{
                0x01, 0x02, 0x01, 0x02,
                0x03, 0x04, 0x03, 0x04
        }, stereo);
    }

    @Test public void rejectsInvalidPcmFrameSizes() {
        assertThrows(IllegalArgumentException.class,
                () -> GatewayService.monoToStereo(new byte[3], new byte[6]));
    }

    @Test public void muteProducesDigitalSilenceInsteadOfHandsetAudio() {
        byte[] stereo = new byte[]{1,2,3,4,5,6,7,8};
        GatewayService.prepareUplinkFrame(new byte[]{1,2,3,4}, stereo, true);
        assertArrayEquals(new byte[8], stereo);
    }

    @Test public void streamingWaveHeaderDescribesStereo16kPcm() {
        byte[] header = GatewayService.streamingWaveHeader();
        assertEquals(44, header.length);
        assertArrayEquals(new byte[]{'R','I','F','F'}, java.util.Arrays.copyOfRange(header, 0, 4));
        assertArrayEquals(new byte[]{'W','A','V','E'}, java.util.Arrays.copyOfRange(header, 8, 12));
        assertEquals(2, header[22]);
        assertEquals(0x80, header[24] & 0xff);
        assertEquals(0x3e, header[25] & 0xff);
        assertEquals(16, header[34]);
    }
}
