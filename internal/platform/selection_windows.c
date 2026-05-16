#include <windows.h>
#include <objbase.h>
#include <oleauto.h>
#include <initguid.h>
#include <uiautomation.h>
#include <stdlib.h>
#include <string.h>

static void DKSTEnsureCoInit(void) {
    HRESULT hr = CoInitializeEx(NULL, COINIT_MULTITHREADED);
    if (hr == RPC_E_CHANGED_MODE) {
        CoInitializeEx(NULL, COINIT_APARTMENTTHREADED);
    }
}

char* DKSTGetSelectedTextUTF8(void) {
    DKSTEnsureCoInit();

    IUIAutomation* pAuto = NULL;
    HRESULT hr = CoCreateInstance(&CLSID_CUIAutomation, NULL,
        CLSCTX_INPROC_SERVER, &IID_IUIAutomation, (void**)&pAuto);
    if (FAILED(hr) || pAuto == NULL) {
        return NULL;
    }

    char* result = NULL;
    IUIAutomationElement* pElem = NULL;
    hr = pAuto->lpVtbl->GetFocusedElement(pAuto, &pElem);
    if (SUCCEEDED(hr) && pElem != NULL) {
        IUIAutomationTextPattern* pTP = NULL;
        hr = pElem->lpVtbl->GetCurrentPatternAs(pElem, UIA_TextPatternId,
            &IID_IUIAutomationTextPattern, (void**)&pTP);
        if (SUCCEEDED(hr) && pTP != NULL) {
            IUIAutomationTextRangeArray* pRanges = NULL;
            hr = pTP->lpVtbl->GetSelection(pTP, &pRanges);
            if (SUCCEEDED(hr) && pRanges != NULL) {
                int rangeCount = 0;
                pRanges->lpVtbl->get_Length(pRanges, &rangeCount);
                if (rangeCount > 0) {
                    IUIAutomationTextRange* pRange = NULL;
                    if (SUCCEEDED(pRanges->lpVtbl->GetElement(pRanges, 0, &pRange)) && pRange != NULL) {
                        BSTR bstr = NULL;
                        if (SUCCEEDED(pRange->lpVtbl->GetText(pRange, -1, &bstr)) && bstr != NULL) {
                            int wlen = (int)SysStringLen(bstr);
                            int u8 = WideCharToMultiByte(CP_UTF8, 0, bstr, wlen, NULL, 0, NULL, NULL);
                            if (u8 > 0) {
                                result = (char*)malloc((size_t)u8 + 1);
                                if (result) {
                                    int written = WideCharToMultiByte(CP_UTF8, 0, bstr, wlen, result, u8, NULL, NULL);
                                    if (written <= 0) {
                                        free(result);
                                        result = NULL;
                                    } else {
                                        result[written] = '\0';
                                    }
                                }
                            } else {
                                result = (char*)malloc(1);
                                if (result) result[0] = '\0';
                            }
                            SysFreeString(bstr);
                        }
                        pRange->lpVtbl->Release(pRange);
                    }
                }
                pRanges->lpVtbl->Release(pRanges);
            }
            pTP->lpVtbl->Release(pTP);
        }
        pElem->lpVtbl->Release(pElem);
    }

    pAuto->lpVtbl->Release(pAuto);
    return result;
}

void DKSTFreeText(char* p) {
    if (p) free(p);
}
